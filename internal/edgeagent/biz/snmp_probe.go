package biz

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

const (
	oidSysName        = "1.3.6.1.2.1.1.5.0"
	oidSysDescription = "1.3.6.1.2.1.1.1.0"
	oidSysObjectID    = "1.3.6.1.2.1.1.2.0"
	oidIfDescr        = "1.3.6.1.2.1.2.2.1.2"
	oidIfType         = "1.3.6.1.2.1.2.2.1.3"
	oidIfPhysAddress  = "1.3.6.1.2.1.2.2.1.6"
	oidIfAdminStatus  = "1.3.6.1.2.1.2.2.1.7"
	oidIfOperStatus   = "1.3.6.1.2.1.2.2.1.8"
	oidIfName         = "1.3.6.1.2.1.31.1.1.1.1"
	oidIfAlias        = "1.3.6.1.2.1.31.1.1.1.18"
	oidIfSpeed        = "1.3.6.1.2.1.2.2.1.5"
	oidIfInErrors     = "1.3.6.1.2.1.2.2.1.14"
	oidIfOutErrors    = "1.3.6.1.2.1.2.2.1.20"
	oidIfHCInOctets   = "1.3.6.1.2.1.31.1.1.1.6"
	oidIfHCOutOctets  = "1.3.6.1.2.1.31.1.1.1.10"
	oidIfHighSpeed    = "1.3.6.1.2.1.31.1.1.1.15"
	oidIPAdEntIfIndex = "1.3.6.1.2.1.4.20.1.2"
	maxSNMPInterfaces = 512
	maxSNMPAddresses  = 2048
)

// ProbeNetworkSNMP performs a bounded read-only identity probe and enriches a
// successful response with interface state and IPv4 management addresses.
func ProbeNetworkSNMP(ctx context.Context, req tunnel.ProbeNetworkSNMPRequest) tunnel.ProbeNetworkSNMPResponse {
	address := strings.TrimSpace(req.Address)
	if address == "" {
		return tunnel.ProbeNetworkSNMPResponse{Error: "SNMP address is required"}
	}
	if req.Port == 0 {
		req.Port = 161
	}
	timeout := 3 * time.Second
	if req.TimeoutMilliseconds > 0 && req.TimeoutMilliseconds <= 15000 {
		timeout = time.Duration(req.TimeoutMilliseconds) * time.Millisecond
	}
	retries := req.Retries
	if retries < 0 || retries > 3 {
		retries = 1
	}

	version := strings.ToLower(strings.TrimSpace(req.Version))
	if version == "" {
		version = "v2c"
	}
	params := &gosnmp.GoSNMP{
		Target:         address,
		Port:           req.Port,
		Timeout:        timeout,
		Retries:        retries,
		MaxOids:        3,
		MaxRepetitions: 25,
		Version:        gosnmp.Version2c,
		Community:      req.Community,
		Context:        ctx,
	}
	if version == "v3" {
		security, flags, err := usmSecurity(req)
		if err != nil {
			return tunnel.ProbeNetworkSNMPResponse{Error: err.Error()}
		}
		params.Version = gosnmp.Version3
		params.SecurityParameters = security
		params.MsgFlags = flags
	} else if version != "v2c" {
		return tunnel.ProbeNetworkSNMPResponse{Error: "SNMP version must be v2c or v3"}
	}
	if version == "v2c" && strings.TrimSpace(req.Community) == "" {
		return tunnel.ProbeNetworkSNMPResponse{Error: "SNMP community is required for v2c"}
	}

	if err := ctx.Err(); err != nil {
		return tunnel.ProbeNetworkSNMPResponse{Error: err.Error()}
	}
	if err := params.Connect(); err != nil {
		return tunnel.ProbeNetworkSNMPResponse{Error: fmt.Sprintf("SNMP connect: %v", err)}
	}
	defer params.Conn.Close()
	result, err := params.Get([]string{oidSysName, oidSysDescription, oidSysObjectID})
	if err != nil {
		return tunnel.ProbeNetworkSNMPResponse{Error: fmt.Sprintf("SNMP get: %v", err)}
	}
	response := tunnel.ProbeNetworkSNMPResponse{OK: true, IPAddress: address}
	response.SNMPEngineID = snmpEngineID(params)
	for _, pdu := range result.Variables {
		value := snmpValue(pdu.Value)
		switch pdu.Name {
		case "." + oidSysName:
			response.SysName = value
		case "." + oidSysDescription:
			response.SysDescription = value
		case "." + oidSysObjectID:
			response.SysObjectID = value
		}
	}
	response.Interfaces = collectSNMPInterfaces(params)
	return response
}

func snmpEngineID(params *gosnmp.GoSNMP) string {
	if params == nil {
		return ""
	}
	security, ok := params.SecurityParameters.(*gosnmp.UsmSecurityParameters)
	if !ok || security.AuthoritativeEngineID == "" {
		return ""
	}
	return hex.EncodeToString([]byte(security.AuthoritativeEngineID))
}

var errInterfaceLimit = errors.New("SNMP interface limit reached")

func collectSNMPInterfaces(params *gosnmp.GoSNMP) []tunnel.NetworkInterfaceReport {
	rows := make(map[int]*tunnel.NetworkInterfaceReport)
	get := func(index int) *tunnel.NetworkInterfaceReport {
		row := rows[index]
		if row == nil {
			row = &tunnel.NetworkInterfaceReport{IfIndex: index}
			rows[index] = row
		}
		return row
	}
	walk := func(oid string, apply func(*tunnel.NetworkInterfaceReport, gosnmp.SnmpPDU)) {
		err := params.BulkWalk(oid, func(pdu gosnmp.SnmpPDU) error {
			index, ok := oidIndex(pdu.Name, oid)
			if !ok {
				return nil
			}
			if _, exists := rows[index]; !exists && len(rows) >= maxSNMPInterfaces {
				return errInterfaceLimit
			}
			apply(get(index), pdu)
			return nil
		})
		if err != nil && !errors.Is(err, errInterfaceLimit) {
			return
		}
	}

	walk(oidIfDescr, func(row *tunnel.NetworkInterfaceReport, pdu gosnmp.SnmpPDU) { row.Name = snmpValue(pdu.Value) })
	walk(oidIfName, func(row *tunnel.NetworkInterfaceReport, pdu gosnmp.SnmpPDU) {
		if name := snmpValue(pdu.Value); name != "" {
			row.Name = name
		}
	})
	walk(oidIfAlias, func(row *tunnel.NetworkInterfaceReport, pdu gosnmp.SnmpPDU) { row.Description = snmpValue(pdu.Value) })
	walk(oidIfType, func(row *tunnel.NetworkInterfaceReport, pdu gosnmp.SnmpPDU) {
		row.InterfaceKind = snmpInterfaceKind(snmpInt(pdu.Value))
	})
	walk(oidIfPhysAddress, func(row *tunnel.NetworkInterfaceReport, pdu gosnmp.SnmpPDU) {
		if value, ok := pdu.Value.([]byte); ok && len(value) > 0 {
			row.MAC = net.HardwareAddr(value).String()
		}
	})
	walk(oidIfAdminStatus, func(row *tunnel.NetworkInterfaceReport, pdu gosnmp.SnmpPDU) {
		row.AdminStatus = snmpInterfaceStatus(snmpInt(pdu.Value))
	})
	walk(oidIfOperStatus, func(row *tunnel.NetworkInterfaceReport, pdu gosnmp.SnmpPDU) {
		row.OperStatus = snmpInterfaceStatus(snmpInt(pdu.Value))
	})
	walk(oidIfSpeed, func(row *tunnel.NetworkInterfaceReport, pdu gosnmp.SnmpPDU) {
		row.SpeedBps = snmpUint64(pdu.Value)
	})
	walk(oidIfHighSpeed, func(row *tunnel.NetworkInterfaceReport, pdu gosnmp.SnmpPDU) {
		if speedMbps := snmpUint64(pdu.Value); speedMbps > 0 {
			row.SpeedBps = speedMbps * 1_000_000
		}
	})
	walk(oidIfHCInOctets, func(row *tunnel.NetworkInterfaceReport, pdu gosnmp.SnmpPDU) { row.InOctets = snmpUint64(pdu.Value) })
	walk(oidIfHCOutOctets, func(row *tunnel.NetworkInterfaceReport, pdu gosnmp.SnmpPDU) { row.OutOctets = snmpUint64(pdu.Value) })
	walk(oidIfInErrors, func(row *tunnel.NetworkInterfaceReport, pdu gosnmp.SnmpPDU) { row.InErrors = snmpUint64(pdu.Value) })
	walk(oidIfOutErrors, func(row *tunnel.NetworkInterfaceReport, pdu gosnmp.SnmpPDU) { row.OutErrors = snmpUint64(pdu.Value) })
	collectSNMPIPv4Addresses(params, rows, get)

	indexes := make([]int, 0, len(rows))
	for index := range rows {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	interfaces := make([]tunnel.NetworkInterfaceReport, 0, len(indexes))
	for _, index := range indexes {
		interfaces = append(interfaces, *rows[index])
	}
	return interfaces
}

func collectSNMPIPv4Addresses(
	params *gosnmp.GoSNMP,
	rows map[int]*tunnel.NetworkInterfaceReport,
	get func(int) *tunnel.NetworkInterfaceReport,
) {
	addressCount := 0
	err := params.BulkWalk(oidIPAdEntIfIndex, func(pdu gosnmp.SnmpPDU) error {
		if addressCount >= maxSNMPAddresses {
			return errInterfaceLimit
		}
		address, ok := oidIPv4Address(pdu.Name, oidIPAdEntIfIndex)
		ifIndex := snmpInt(pdu.Value)
		if !ok || ifIndex <= 0 {
			return nil
		}
		if _, exists := rows[ifIndex]; !exists && len(rows) >= maxSNMPInterfaces {
			return errInterfaceLimit
		}
		row := get(ifIndex)
		for _, existing := range row.Addresses {
			if existing == address {
				return nil
			}
		}
		row.Addresses = append(row.Addresses, address)
		addressCount++
		return nil
	})
	if err != nil && !errors.Is(err, errInterfaceLimit) {
		return
	}
}

func oidIndex(name, root string) (int, bool) {
	normalizedName := strings.TrimPrefix(name, ".")
	normalizedRoot := strings.TrimPrefix(root, ".")
	prefix := normalizedRoot + "."
	if !strings.HasPrefix(normalizedName, prefix) {
		return 0, false
	}
	suffix := strings.TrimPrefix(normalizedName, prefix)
	if strings.Contains(suffix, ".") {
		return 0, false
	}
	index, err := strconv.Atoi(suffix)
	return index, err == nil && index > 0
}

func oidIPv4Address(name, root string) (string, bool) {
	normalizedName := strings.TrimPrefix(name, ".")
	normalizedRoot := strings.TrimPrefix(root, ".")
	prefix := normalizedRoot + "."
	if !strings.HasPrefix(normalizedName, prefix) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(normalizedName, prefix), ".")
	if len(parts) != net.IPv4len {
		return "", false
	}
	octets := make(net.IP, net.IPv4len)
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || value > 255 {
			return "", false
		}
		octets[index] = byte(value)
	}
	return octets.String(), true
}

func snmpInt(value any) int {
	integer := gosnmp.ToBigInt(value)
	if integer == nil || !integer.IsInt64() {
		return 0
	}
	return int(integer.Int64())
}

func snmpUint64(value any) uint64 {
	integer := gosnmp.ToBigInt(value)
	if integer == nil || integer.Sign() < 0 || !integer.IsUint64() {
		return 0
	}
	return integer.Uint64()
}

func snmpInterfaceStatus(value int) string {
	switch value {
	case 1:
		return "up"
	case 2:
		return "down"
	case 3:
		return "testing"
	default:
		return "unknown"
	}
}

func snmpInterfaceKind(value int) string {
	switch value {
	case 6:
		return "ethernet"
	case 24:
		return "loopback"
	case 53:
		return "virtual"
	case 131:
		return "tunnel"
	case 161:
		return "lag"
	default:
		if value <= 0 {
			return "unknown"
		}
		return fmt.Sprintf("ifType %d", value)
	}
}

func usmSecurity(req tunnel.ProbeNetworkSNMPRequest) (*gosnmp.UsmSecurityParameters, gosnmp.SnmpV3MsgFlags, error) {
	if strings.TrimSpace(req.Username) == "" {
		return nil, gosnmp.NoAuthNoPriv, fmt.Errorf("SNMP username is required for v3")
	}
	security := &gosnmp.UsmSecurityParameters{UserName: req.Username}
	flags := gosnmp.NoAuthNoPriv
	authProtocol := normalizeSNMPProtocol(req.AuthProtocol)
	switch authProtocol {
	case "", "none", "noauth":
		security.AuthenticationProtocol = gosnmp.NoAuth
	case "md5":
		security.AuthenticationProtocol = gosnmp.MD5
	case "sha", "sha1":
		security.AuthenticationProtocol = gosnmp.SHA
	case "sha224":
		security.AuthenticationProtocol = gosnmp.SHA224
	case "sha256":
		security.AuthenticationProtocol = gosnmp.SHA256
	case "sha384":
		security.AuthenticationProtocol = gosnmp.SHA384
	case "sha512":
		security.AuthenticationProtocol = gosnmp.SHA512
	default:
		return nil, gosnmp.NoAuthNoPriv, fmt.Errorf("unsupported SNMP auth protocol %q", req.AuthProtocol)
	}
	if security.AuthenticationProtocol != gosnmp.NoAuth {
		if strings.TrimSpace(req.AuthSecret) == "" {
			return nil, gosnmp.NoAuthNoPriv, fmt.Errorf("SNMP auth secret is required for %s", req.AuthProtocol)
		}
		security.AuthenticationPassphrase = req.AuthSecret
		flags = gosnmp.AuthNoPriv
	}

	privacyProtocol := normalizeSNMPProtocol(req.PrivacyProtocol)
	switch privacyProtocol {
	case "", "none", "nopriv":
		security.PrivacyProtocol = gosnmp.NoPriv
	case "des":
		security.PrivacyProtocol = gosnmp.DES
	case "aes", "aes128":
		security.PrivacyProtocol = gosnmp.AES
	case "aes192":
		security.PrivacyProtocol = gosnmp.AES192
	case "aes256":
		security.PrivacyProtocol = gosnmp.AES256
	case "aes192c":
		security.PrivacyProtocol = gosnmp.AES192C
	case "aes256c":
		security.PrivacyProtocol = gosnmp.AES256C
	default:
		return nil, gosnmp.NoAuthNoPriv, fmt.Errorf("unsupported SNMP privacy protocol %q", req.PrivacyProtocol)
	}
	if security.PrivacyProtocol != gosnmp.NoPriv {
		if security.AuthenticationProtocol == gosnmp.NoAuth {
			return nil, gosnmp.NoAuthNoPriv, fmt.Errorf("SNMP privacy requires authentication")
		}
		if strings.TrimSpace(req.PrivacySecret) == "" {
			return nil, gosnmp.NoAuthNoPriv, fmt.Errorf("SNMP privacy secret is required for %s", req.PrivacyProtocol)
		}
		security.PrivacyPassphrase = req.PrivacySecret
		flags = gosnmp.AuthPriv
	}
	return security, flags, nil
}

func normalizeSNMPProtocol(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("-", "", "_", "", " ", "")
	return replacer.Replace(value)
}

func snmpValue(value any) string {
	switch v := value.(type) {
	case []byte:
		return strings.TrimSpace(string(v))
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
