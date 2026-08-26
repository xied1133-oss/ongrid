// Package cmdpolicy is the policy + sandbox layer that gates which
// shell commands the edge will execute on behalf of an LLM-driven tool
// (currently the bash skill — internal/edgeagent/bash). The policy is
// intentionally decoupled from any single tool: future skills (plugin
// custom shells, guarded mutating bash, etc.) can compose a different
// Policy + the same Sandbox runner.
//
// Design pillars:
//
//  1. Class-based binary taxonomy. Each binary belongs to one of
//     {read-fs, read-system, mixed, network, denied}. read-* classes
//     are unconditionally allowed; mixed/network classes carry per-arg
//     ReadOnlyMatchers / WriteMatchers so we can split iptables -L from
//     iptables -A; denied is always rejected.
//
//  2. The default policy (DefaultReadOnly) ships a curated baseline
//     covering common diagnostic binaries (ps, df, lsof, ss, find,
//     du, awk, sed, iptables -L, ...) and refuses every shell /
//     scripting language / file-mutating binary outright.
//
//  3. Operators may override via /etc/ongrid-edge/bash-policy.yaml. The
//     YAML is merged on top of the default — an operator can extend
//     the binary list, tighten matchers, or shrink the path allowlist.
//
//  4. Path validation REUSES host_files.SandboxConfig via the
//     PathValidator interface — there is no separate path allowlist
//     code path here. cmdpolicy treats any /-prefixed argv token as a
//     path candidate and asks the validator.
//
//  5. Every parsed argv is bounded: no shell metacharacters / no
//     redirects / no backticks / no command substitution / one or
//     more pipe segments only. A tokenizer in parse.go enforces this
//     (we deliberately do NOT shell out to /bin/sh — exec.Command
//     receives a clean argv array).
//
// The package never executes commands by itself; Sandbox.Exec does. A
// caller may also use Policy.Decide standalone (e.g. for a dry-run UI
// preview or audit query) without touching Sandbox.
package cmdpolicy

import "time"

// BinaryClass groups binaries by behaviour. Read-only classes are
// always allowed (they cannot mutate state in any combination of args);
// mixed/network classes have read-only sub-arguments and write/dangerous
// sub-arguments that we discriminate via matchers; denied is the
// blanket reject — used both for shells / scripting languages (which
// can run anything) and for irreversibly destructive binaries.
type BinaryClass string

const (
	// ClassReadFS is filesystem-read-only: ls / cat / find / awk / sed
	// (with -i denied) etc. By definition no member can mutate state.
	ClassReadFS BinaryClass = "read-fs"

	// ClassReadSystem is system-state-read-only: ps / df / free / ss /
	// lsof / journalctl etc. These bind to host kernel/runtime state
	// without changing it.
	ClassReadSystem BinaryClass = "read-system"

	// ClassMixed is "depends on the args" — iptables -L is read,
	// iptables -A is write. Per-argv discrimination happens through
	// ReadOnlyMatchers + WriteMatchers (write match wins).
	ClassMixed BinaryClass = "mixed"

	// ClassNetwork is outbound-network: nc / curl / dig / ping. These
	// touch the world; they are gated by NetworkHostAllowlist (the
	// secondary policy.NetworkHostAllowlist field) on top of any
	// per-binary matchers.
	ClassNetwork BinaryClass = "network"

	// ClassDenied is unconditional reject. Shells (bash/sh), scripting
	// languages (python/perl), destructive fs ops (rm/dd), permission
	// changes (chmod/chown), system mutators (shutdown/reboot), auth
	// mutators (useradd/passwd) all live here.
	ClassDenied BinaryClass = "denied"
)

// ArgMatcher describes a single matching rule against an argv slice
// (after argv[0] = the binary itself). A matcher hits when ANY of its
// non-empty sub-rules matches. The three sub-rules are:
//
//   - AnyFlag: any of the listed tokens appears anywhere in argv[1:].
//     Used for "is iptables in -L mode" → AnyFlag: ["-L", "--list"].
//
//   - Subcmd: argv[1] equals this token exactly. Used for
//     "systemctl status" / "ip addr" — the discriminator is positional.
//
//   - SubcmdPath: argv[1..len(SubcmdPath)] equals this slice. Used for
//     "tc qdisc show" — a multi-token positional discriminator.
//
// A matcher with all three sub-rules empty is treated as a catch-all
// (matches any argv); useful when WriteMatchers needs to say "anything
// not covered by ReadOnlyMatchers is a write" (kill defaults to send).
type ArgMatcher struct {
	AnyFlag    []string
	Subcmd     string
	SubcmdPath []string
}

// BinaryPolicy is the per-binary rule set. Bin is the basename; the
// matching tokenizer keys off basename so /usr/sbin/iptables and
// /sbin/iptables both resolve to the iptables policy. AbsPath is the
// resolved absolute executable path filled by discoverBin at policy
// construction; an empty AbsPath means the binary was not found on
// this host (the policy then refuses any call to it with a clean
// "not installed" reason).
type BinaryPolicy struct {
	Bin     string      // basename, e.g. "iptables"
	AbsPath string      // resolved absolute path; "" when not found
	Class   BinaryClass // see consts above

	// ReadOnlyMatchers / WriteMatchers apply only to ClassMixed and
	// ClassNetwork. Empty slices mean "no discrimination at this side".
	// Decision rule when both are populated:
	//   1. If any DeniedArgs token appears in argv → REJECT.
	//   2. If any WriteMatcher matches → REJECT (write semantics).
	//   3. If any ReadOnlyMatcher matches → ALLOW.
	//   4. Else fall through to a class default:
	//      - mixed   → REJECT (safer when the argv is ambiguous).
	//      - network → ALLOW IFF target host passes the host allowlist.
	ReadOnlyMatchers []ArgMatcher
	WriteMatchers    []ArgMatcher

	// DeniedArgs is a global token blacklist for this binary regardless
	// of class. Used for "find -delete", "sed -i", "awk system(", etc.
	// Substring match (NOT exact equality) so "system(" tags an awk
	// program string that calls system; "-delete" tags any -delete arg.
	DeniedArgs []string
}

// Policy is the full rule set: per-binary policies keyed by basename,
// plus global guardrails (caps, timeout, max args, network-host
// allowlist, path allowlist).
type Policy struct {
	bins map[string]*BinaryPolicy

	// NetworkHostAllowlist gates outbound-network binaries
	// (ClassNetwork). Each entry is either a CIDR (10.0.0.0/8) or a
	// hostname suffix (".internal" matches any host ending in
	// ".internal"). Empty list = deny ALL outbound. Operators set this
	// when they want their LLM to be able to ping internal services.
	NetworkHostAllowlist []string

	// StdoutCap / StderrCap bound process output. Excess is truncated
	// and the response carries Truncated=true so the LLM can tell.
	StdoutCap int
	StderrCap int

	// Timeout is the per-call hard ceiling. The Sandbox wraps the call
	// in context.WithTimeout; on timeout the process is killed and a
	// non-zero exit is reported.
	Timeout time.Duration

	// MaxArgs caps len(argv) per pipe-segment. Defends against ARG_MAX
	// flooding and dramatically narrows the surface a smuggled
	// argument can ride on.
	MaxArgs int

	// PathAllowlist is the set of absolute path prefixes commands are
	// permitted to reference. Any /-prefixed argv token is checked
	// against this list (via the PathValidator the Sandbox carries).
	// Empty list = no path validation (NOT recommended; the default
	// policy ships a curated set).
	PathAllowlist []string
}

// Decision is the result of Policy.Decide / Sandbox.Decide. When Allow
// is false, Reason carries a human-readable string that is also
// suitable to surface to the LLM ("binary 'rm' is in denied class")
// so the model can correct. Bin + Argv are present whenever the
// command parsed cleanly enough to identify the first segment, even
// if rejected — the LLM benefits from seeing what we thought it asked
// for, not just "no".
type Decision struct {
	Allow  bool
	Reason string
	// Segments is the parsed pipeline, one argv per segment.
	// Always emitted when parsing succeeded; nil otherwise.
	Segments [][]string
}
