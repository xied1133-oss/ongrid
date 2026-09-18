#!/usr/bin/env bash
# configure-qms-topology.sh — 汽车质量管理平台（jinhui + deepway）拓扑批量配置
#
# 依据业务开发提供的架构图 / pom 依赖 / 数据流关系，向 Ongrid Manager 批量写入
# 拓扑节点与关系。幂等：已存在的节点按名复用，已存在的边打 SKIP 继续。
#
# 建模口径（与 Ongrid 拓扑模型语义一致）：
#   member_of     服务 → 应用            聚合，不传故障（UI 虚线）
#   deployed_on   服务/中间件 → 宿主机    传故障（主机挂 → 全体受影响）
#   routes_to     网关 → 后端            传故障，顺向（网关挂 → 后端不可达）
#   depends_on    调用方 → 被调方        传故障，逆向（被调方挂 → 调用方受影响）
#   integrates_with（自定义）外部异步集成  不传故障（对方挂只缺数据，不故障）
#
# 用法：
#   1) 取 token：curl -sk -X POST https://<host>/api/v1/auth/login \
#        -H 'Content-Type: application/json' \
#        -d '{"email":"<管理员邮箱>","password":"<密码>"}' | jq -r .access_token
#   2) ONGRID_TOKEN=<token> HOST_NODE=<宿主机device节点名> ./configure-qms-topology.sh
#      （HOST_NODE 留空时脚本会列出现有 device 节点供选择后退出）
#
# 发送方清单（SENDERS）为按数据流推断的默认值，跑前在业务仓库核对：
#   grep -rl "EsbSendProvider\|SendZqyProvider" --include='*.java' . | cut -d/ -f2 | sort -u

set -euo pipefail

BASE="${ONGRID_BASE:-https://10.51.13.7/api/v1/topology}"
TOKEN="${ONGRID_TOKEN:?need ONGRID_TOKEN (login access_token)}"
HOST_NODE="${HOST_NODE:-}"
AUTH=(-H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json')

# ---------- helpers ----------

# node_id <type> <name> [props_json]：按名查节点，不存在则创建，输出 id
node_id() {
    local type="$1" name="$2" props="${3:-}" id
    id=$(curl -sk "${AUTH[@]}" "$BASE/nodes?q=$(printf '%s' "$name" | sed 's/ /%20/g')&limit=200" \
        | jq -r --arg n "$name" '(.items // .data.items // [])[] | select(.name==$n) | .id' | head -1)
    if [[ -z "$id" ]]; then
        local body="{\"type\":\"$type\",\"name\":\"$name\"}"
        [[ -n "$props" ]] && body="{\"type\":\"$type\",\"name\":\"$name\",\"props\":$props}"
        id=$(curl -sk -X POST "${AUTH[@]}" "$BASE/nodes" -d "$body" | jq -r '.id // .data.id')
        echo "new  node $type/$name #$id" >&2
    fi
    [[ -n "$id" && "$id" != "null" ]] || { echo "FATAL: cannot resolve node $name" >&2; exit 1; }
    printf '%s' "$id"
}

# rel <src> <dst> <type>：建边，重复/失败打 SKIP 不中断
rel() {
    if curl -sk -X POST "${AUTH[@]}" "$BASE/relations" \
        -d "{\"src_id\":${ID[$1]},\"dst_id\":${ID[$2]},\"type\":\"$3\"}" \
        | jq -e '.id // .data.id' >/dev/null 2>&1; then
        echo "ok   $1 -$3-> $2"
    else
        echo "SKIP $1 -$3-> $2 (已存在或失败)"
    fi
}

# register_rel_type <json>：自定义关系类型，已存在则跳过
register_rel_type() {
    local name
    name=$(jq -r .name <<<"$1")
    if curl -sk "${AUTH[@]}" "$BASE/relation-types/$name" | jq -e '.name // .data.name' >/dev/null 2>&1; then
        echo "SKIP relation-type $name (已存在)"
    else
        curl -sk -X POST "${AUTH[@]}" "$BASE/relation-types" -d "$1" \
            | jq -e '.name // .data.name' >/dev/null && echo "ok   relation-type $name"
    fi
}

# ---------- 宿主机 device 节点 ----------

if [[ -z "$HOST_NODE" ]]; then
    echo "现有 device 节点：" >&2
    curl -sk "${AUTH[@]}" "$BASE/nodes?type=device&limit=100" \
        | jq -r '(.items // .data.items // [])[] | "  #\(.id) \(.name)"' >&2
    echo "请用 HOST_NODE=<节点名> 重跑" >&2
    exit 2
fi

# ---------- 节点 ----------

declare -A ID
ID[platform]=$(node_id app "汽车质量管理平台" '{"domain":"jinhui+deepway 一体化质量平台"}')

# 平台层 + 业务层（jinhui-common 为编译期公共库、deepway-st 未部署，均不建节点）
ID[jinhui-gateway]=$(node_id service jinhui-gateway '{"port":3000,"domain":"API网关/路由/鉴权/限流"}')
ID[jinhui-system]=$(node_id service jinhui-system '{"port":8888,"domain":"系统底座/认证/权限/字典/门户"}')
ID[jinhui-inter]=$(node_id service jinhui-inter '{"port":8885,"domain":"外部接口集成枢纽(ESB收发件箱)"}')
ID[jinhui-task]=$(node_id service jinhui-task '{"port":8887,"domain":"XXL-Job执行器/约60个定时任务"}')
ID[jinhui-job-admin]=$(node_id service jinhui-job-admin '{"port":8889,"domain":"XXL-Job调度中心(Java8定制版)"}')
ID[deepway-base-data]=$(node_id service deepway-base-data '{"port":8890,"domain":"主数据/工厂树/车型/零部件/SAP同步"}')
ID[deepway-mqm]=$(node_id service deepway-mqm '{"port":8891,"domain":"制造质量核心(IQC/焊装/涂装/围堵/断点)"}')
ID[deepway-qcts]=$(node_id service deepway-qcts '{"port":8892,"domain":"质量改进(8D/一致性/召回)"}')
ID[deepway-ppap]=$(node_id service deepway-ppap '{"port":8893,"domain":"PPAP/OTS样件/4M变更"}')
ID[deepway-am]=$(node_id service deepway-am '{"port":8894,"domain":"电池追溯/合规上报/合格证"}')
ID[deepway-tqc]=$(node_id service deepway-tqc '{"port":8895,"domain":"售后质量(索赔/追偿/IPTV/CPV)"}')
ID[deepway-detection]=$(node_id service deepway-detection '{"port":8896,"domain":"检测管理(TSP下发/Kafka回传CAN)"}')

# 中间件（均在本宿主机）
ID[nacos]=$(node_id cluster nacos '{"role":"注册中心/配置中心"}')
ID[mysql]=$(node_id cluster mysql '{"role":"主数据库(含MID_/MARK_中间表)"}')
ID[redis]=$(node_id cluster redis '{"role":"缓存/分布式锁/单号生成"}')
ID[minio]=$(node_id cluster minio '{"role":"对象存储/文件"}')
ID[kafka]=$(node_id cluster kafka '{"role":"消息队列(detection CAN信号回传)"}')

ID[host]=$(node_id device "$HOST_NODE")

# 外部系统（异步集成为主；props 标记 external 便于 UI 过滤）
EXT_PROPS='{"external":true}'
for n in MES MOM WMS JAC SRM ASP DCP CRM iPass 中汽研 TSP-IoV CPM IAM OA SAP; do
    ID[$n]=$(node_id service "$n" "$EXT_PROPS")
done

# ---------- 自定义关系类型（外部异步集成，不传故障） ----------

register_rel_type '{
  "name":"integrates_with","display_name":"集成","display_name_en":"Integrates with",
  "propagates_failure":false,"direction":"bidirectional",
  "semantics_tag":"observation",
  "description":"与外部系统的异步报文/上报集成，不传播故障"
}'

# ---------- 边：主干 ----------

SVCS="jinhui-gateway jinhui-system jinhui-inter jinhui-task jinhui-job-admin
deepway-base-data deepway-mqm deepway-qcts deepway-ppap deepway-am deepway-tqc deepway-detection"

for s in $SVCS; do
    rel "$s" platform member_of       # 服务属于应用
    rel "$s" host     deployed_on     # 部署在宿主机
    rel "$s" mysql    depends_on      # 共享数据层
    rel "$s" redis    depends_on
    rel "$s" nacos    depends_on
    rel "$s" minio    depends_on
done
for c in nacos mysql redis minio kafka; do rel "$c" host deployed_on; done

# 网关路由（已确认：全部 11 个后端都走网关）
for s in jinhui-system jinhui-inter jinhui-task jinhui-job-admin \
         deepway-base-data deepway-mqm deepway-qcts deepway-ppap deepway-am \
         deepway-tqc deepway-detection; do
    rel jinhui-gateway "$s" routes_to
done

# Feign 调用（调用方 -depends_on-> 被调方；pom 验证过的环保留双向边）
rel jinhui-gateway  jinhui-system    depends_on
rel jinhui-task     jinhui-job-admin depends_on
for s in jinhui-system jinhui-inter deepway-base-data deepway-mqm deepway-qcts \
         deepway-ppap deepway-am deepway-tqc deepway-detection; do
    rel jinhui-task "$s" depends_on
done
for s in deepway-mqm deepway-am deepway-qcts deepway-tqc deepway-base-data; do
    rel jinhui-inter "$s" depends_on          # 入向：报文转交业务 Provider 落库
done
for s in deepway-base-data deepway-am deepway-qcts deepway-detection jinhui-inter; do
    rel deepway-mqm "$s" depends_on           # 业务枢纽 mqm 的出向依赖
done
for s in deepway-base-data deepway-qcts deepway-tqc deepway-detection jinhui-inter; do
    rel "$s" deepway-mqm depends_on           # 反向依赖 mqm（环）
done
rel deepway-qcts      deepway-base-data depends_on
rel deepway-ppap      deepway-base-data depends_on
rel deepway-am        deepway-base-data depends_on
rel deepway-detection deepway-base-data depends_on
rel deepway-detection deepway-am        depends_on
rel deepway-base-data deepway-ppap      depends_on
rel jinhui-system     deepway-mqm       depends_on   # 门户指标聚合
rel jinhui-system     deepway-qcts      depends_on
rel deepway-detection kafka             depends_on   # CAN 信号回传消费

# 出向发送方 → inter（统一调 inter 发送接口；清单跑前用 grep 核对）
SENDERS="jinhui-task deepway-am deepway-mqm deepway-tqc deepway-base-data"
for s in $SENDERS; do rel "$s" jinhui-inter depends_on; done

# ---------- 边：外部集成层 ----------

# ESB 通道统一挂 inter（入向接收 + 出向发送）
for n in MES MOM WMS JAC DCP SRM CRM CPM ASP SAP iPass 中汽研; do
    rel jinhui-inter "$n" integrates_with
done
rel deepway-detection TSP-IoV integrates_with   # 车联网独立链路（计划下发/ASC下载）
rel jinhui-system IAM depends_on                # 唯一同步强依赖：登录认证

# 审批流：common 工作流封装直推 OA（不经 ESB，异步）
for s in jinhui-system deepway-base-data deepway-mqm deepway-qcts \
         deepway-ppap deepway-am deepway-tqc deepway-detection; do
    rel "$s" OA integrates_with
done

echo
echo "完成。拓扑页验证："
echo "  1) 聚焦应用「汽车质量管理平台」+ 关系过滤「只看传故障的」"
echo "  2) 点 mysql  → 影响面应含全部 12 个服务"
echo "  3) 点 gateway → 影响面应含全部 11 个后端"
echo "  4) 点 job-admin → 影响面应含 jinhui-task，不含 gateway"
