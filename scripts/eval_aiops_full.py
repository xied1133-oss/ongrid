#!/usr/bin/env python3
"""Full-spectrum E2E suite — 24 cases across 6 AIOps domains.

Each case is independent (fresh session). The runner captures:
  - tool call sequence
  - elapsed wall-clock
  - final assistant content
  - whether dispatch happened (AgentTool seen)
  - whether the case hit the graceful max-steps message
"""
import json, os, ssl, time, urllib.request

API = "https://127.0.0.1:8443/api/v1"
CTX = ssl.create_default_context(); CTX.check_hostname = False; CTX.verify_mode = ssl.CERT_NONE
OUT = "/tmp/evalFULL"; os.makedirs(OUT, exist_ok=True)


def http(method, path, token=None, body=None, timeout=300):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(API + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, context=CTX, timeout=timeout) as r:
        return json.loads(r.read())


tok = http("POST", "/auth/login", body={"email": "admin@example.com", "password": "27UUl6Ewakh1SPwvlyaG"})["access_token"]

CASES = [
    # ---------------- 计算 / compute (5) ----------------
    {"id": "C1-cpu-top", "domain": "compute", "prompt": "@device:1 CPU 看起来在跑负载，帮我找出 top 5 抢资源的进程，看看是不是某个跑飞了。", "expect": "specialist-compute"},
    {"id": "C2-mem-leak", "domain": "compute", "prompt": "@device:1 内存使用率怎么样？最近 1 小时有没有持续上涨的迹象（怀疑泄漏）？", "expect": "specialist-compute"},
    {"id": "C3-load-vs-cpu", "domain": "compute", "prompt": "@device:1 系统 load 在 15 分钟内涨了一倍，但 CPU% 并不高。是 IO 阻塞还是别的原因？", "expect": "specialist-compute (可能 + specialist-disk)"},
    {"id": "C4-oom-trace", "domain": "compute", "prompt": "@device:1 帮我查最近有没有被 OOM killer 杀过的进程。dmesg 或 journalctl 都行。", "expect": "specialist-compute or specialist-ops"},
    {"id": "C5-steal-time", "domain": "compute", "prompt": "@device:1 这是虚机吧？看下 vmstat 的 steal 字段，宿主有没有抢资源。", "expect": "specialist-compute"},

    # ---------------- 存储 / storage (5) ----------------
    {"id": "S1-top-dir", "domain": "storage", "prompt": "@device:1 根分区使用 80%，哪个目录是大头？", "expect": "specialist-disk"},
    {"id": "S2-largest-files", "domain": "storage", "prompt": "@device:1 /var/log 目录下最大的 5 个文件，给出大小和最后修改时间。", "expect": "specialist-disk"},
    {"id": "S3-inode", "domain": "storage", "prompt": "@device:1 inode 是不是快耗尽？df -i 看一下。", "expect": "specialist-disk or specialist-ops"},
    {"id": "S4-stat-specific", "domain": "storage", "prompt": "@device:1 帮我看下 /opt/ongrid 这个目录的总大小、文件数、最后修改时间。", "expect": "specialist-disk"},
    {"id": "S5-disk-io", "domain": "storage", "prompt": "@device:1 磁盘 IO 延迟最近 5 分钟有没有飙高？看下 node_disk_io_time_seconds_total。", "expect": "specialist-disk or specialist-sre (PromQL)"},

    # ---------------- 网络 / network (5) ----------------
    {"id": "N1-dns", "domain": "network", "prompt": "@device:1 测一下 google.com 的 DNS 解析能不能成功，多久能拿到答案。", "expect": "specialist-network"},
    {"id": "N2-tcp-probe", "domain": "network", "prompt": "@device:1 测一下到 8.8.8.8:443 的 TCP 是不是通的。", "expect": "specialist-network"},
    {"id": "N3-iptables", "domain": "network", "prompt": "@device:1 当前 iptables 规则列出来给我看下，有没有什么 DROP / REJECT 的。", "expect": "specialist-network"},
    {"id": "N4-routes", "domain": "network", "prompt": "@device:1 默认网关是什么？路由表给我看下。", "expect": "specialist-network"},
    {"id": "N5-iface-drops", "domain": "network", "prompt": "@device:1 哪个网卡有丢包 / 重传？给出 rx/tx 错误计数。", "expect": "specialist-network"},

    # ---------------- 虚拟化 / virtualization (3) ----------------
    {"id": "V1-docker-containers", "domain": "virtualization", "prompt": "@device:1 上跑了哪些 docker 容器？分别占多少 CPU / 内存？", "expect": "specialist-ops or specialist-compute"},
    {"id": "V2-hypervisor", "domain": "virtualization", "prompt": "@device:1 这台机器是物理机还是虚机？如果是虚机，hypervisor 是什么？", "expect": "specialist-compute or specialist-ops"},
    {"id": "V3-cgroup", "domain": "virtualization", "prompt": "@device:1 看下 cgroup 内存限制是怎么配的，有没有进程贴着 limit 跑。", "expect": "specialist-compute or specialist-ops"},

    # ---------------- IO (2) ----------------
    {"id": "IO1-iostat", "domain": "io", "prompt": "@device:1 跑下 iostat 看磁盘队列长度，最近 5 秒采样几次。", "expect": "specialist-ops or specialist-disk"},
    {"id": "IO2-iowait-procs", "domain": "io", "prompt": "@device:1 现在有没有进程卡在 iowait（D 状态）？", "expect": "specialist-compute"},

    # ---------------- 综合 / integrated (4) ----------------
    {"id": "M1-net-and-disk", "domain": "integrated", "prompt": "@device:1 我担心它有问题，帮我从网络和磁盘两个方面分别看下。", "expect": "cross-domain → specialist-network + specialist-disk"},
    {"id": "M2-incident-rca", "domain": "integrated", "prompt": "incident 20 的根因到底是什么？请关联指标 + 日志 + 进程给我详细分析。", "expect": "incident-investigator"},
    {"id": "M3-cluster-health", "domain": "integrated", "prompt": "整个集群最近 1 小时的健康度怎么样？哪条告警最值得现在响应？", "expect": "specialist-sre"},
    {"id": "M4-full-checkup", "domain": "integrated", "prompt": "@device:1 给我一个综合体检报告：CPU / 内存 / 磁盘 / 网络 / 服务状态都要覆盖。", "expect": "multi-specialist dispatch (3+)"},
]

results = []
for i, c in enumerate(CASES, 1):
    sess = http("POST", "/chat/sessions", token=tok, body={"title": "evalFULL-" + c["id"]})
    sid = sess["id"]
    t0 = time.time()
    print(f"[{time.strftime('%H:%M:%S')}] {i}/{len(CASES)} {c['id']} ({c['domain']}) ...", flush=True)
    try:
        r = http("POST", f"/chat/sessions/{sid}/messages", token=tok, body={"content": c["prompt"]}, timeout=240)
    except Exception as e:
        r = {"_err": str(e)}
    elapsed = round(time.time() - t0, 1)
    hist = http("GET", f"/chat/sessions/{sid}/messages", token=tok)
    open(f"{OUT}/{c['id']}.json", "w").write(json.dumps({"case": c, "sid": sid, "elapsed_s": elapsed, "reply": r, "history": hist}, ensure_ascii=False, indent=2))
    tools = [m.get("tool_name") for m in hist.get("items", []) if m.get("role") == "tool"]
    dispatched = "AgentTool" in tools
    print(f"  done {c['id']:24s} {elapsed:>5.1f}s  tools={len(tools):>2}  dispatched={dispatched}", flush=True)
    results.append({"id": c["id"], "domain": c["domain"], "elapsed_s": elapsed, "tools": len(tools), "dispatched": dispatched, "tool_list": tools})

with open(f"{OUT}/_summary.json", "w") as f:
    json.dump(results, f, ensure_ascii=False, indent=2)

print()
print("=" * 80)
print("SUMMARY by domain:")
by_domain = {}
for r in results:
    by_domain.setdefault(r["domain"], []).append(r)
for dom, rs in by_domain.items():
    avg_t = sum(x["elapsed_s"] for x in rs) / len(rs)
    avg_tools = sum(x["tools"] for x in rs) / len(rs)
    disp_rate = sum(1 for x in rs if x["dispatched"]) / len(rs)
    print(f"  {dom:15s} n={len(rs)}  avg_time={avg_t:>5.1f}s  avg_tools={avg_tools:>4.1f}  dispatch={disp_rate*100:>4.0f}%")
