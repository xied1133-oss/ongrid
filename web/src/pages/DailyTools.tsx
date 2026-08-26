import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import {
  Activity,
  ChevronDown,
  Copy,
  FileCode2,
  Loader2,
  PanelRightOpen,
  Play,
  RadioTower,
  Search,
  Square,
  TerminalSquare,
  X,
} from 'lucide-react';
import { listEdges, type Edge } from '@/api/edges';
import {
  cancelOperatorRun,
  createOperatorRun,
  getOperatorRun,
  listOperatorNetNS,
  streamOperatorRunEvents,
  type OperatorCommand,
  type OperatorRun,
  type OperatorRunEvent,
} from '@/api/operatorRuns';
import {
  cancelPacketCapture,
  createPacketCaptureSession,
  getPacketCapture,
  packetCaptureArtifactID,
  refreshPacketCaptureSession,
  stopPacketCapture,
  stopPacketCaptureSession,
  type PacketCapture,
  type PacketCaptureSession,
  type PacketCaptureState,
} from '@/api/packetCaptures';
import { ApiError } from '@/api/client';
import { XTerminal, type XTerminalApi } from '@/components/XTerminal';
import { Button, Card, Chip, EmptyState, PageHeader } from '@/components/ui';
import { cn } from '@/lib/cn';
import { useI18n } from '@/i18n/locale';

type ToolKey = 'ping' | 'dns' | 'tcp' | 'http' | 'capture';
type RunStatus = 'running' | 'success' | 'partial' | 'error';
type ProbeStatus = 'queued' | 'running' | 'success' | 'error';
type CaptureQuickStatus = 'starting' | 'capturing' | 'stopping' | 'ready' | 'cancelled' | 'error';
type RunLogStream = 'system' | 'stdout' | 'stderr' | 'status';

type RunLogEvent = {
  id: string;
  ts: string;
  stream: RunLogStream;
  message: string;
  edgeLabel?: string;
};

type ProbeResult = {
  edgeID: number;
  edgeLabel: string;
  status: ProbeStatus;
  startedAt?: string;
  finishedAt?: string;
  durationMs?: number;
  result?: unknown;
  error?: string;
};

type ToolRun = {
  id: string;
  tool: Exclude<ToolKey, 'capture'>;
  title: string;
  target: string;
  status: RunStatus;
  startedAt: string;
  finishedAt?: string;
  pinned?: boolean;
  results: ProbeResult[];
  logs: RunLogEvent[];
};

type CaptureQuickRun = {
  id: string;
  status: CaptureQuickStatus;
  sessionID?: string;
  title: string;
  target: string;
  edgeLabels: string[];
  startedAt: string;
  finishedAt?: string;
  captureIDs: number[];
  link: string;
  members: CaptureQuickMember[];
  logs: RunLogEvent[];
  error?: string;
};

type CaptureQuickMember = {
  id: number;
  edgeLabel: string;
  state: PacketCaptureState | string;
  capturedPackets?: number;
  capturedBytes?: number;
  livePreview?: string[];
  startedAt?: string;
  finishedAt?: string;
  error?: string;
};

type PingForm = { host: string; count: string; timeout_ms: string };
type DNSForm = { host: string; timeout_ms: string };
type TCPForm = { host: string; port: string; timeout_ms: string };
type HTTPForm = { url: string; method: 'HEAD' | 'GET'; timeout_ms: string; skip_tls: boolean };
type ExecAdvancedForm = { namespace: string };
type CaptureForm = {
  interface_name: string;
  filter: string;
  duration_seconds: string;
  max_bytes_mb: string;
  max_packets: string;
  snaplen: string;
  promiscuous: boolean;
  title: string;
};

const TOOLS: Array<{ key: ToolKey; zh: string; en: string }> = [
  { key: 'ping', zh: 'Ping', en: 'Ping' },
  { key: 'dns', zh: 'Dig', en: 'Dig' },
  { key: 'tcp', zh: 'TCP', en: 'TCP' },
  { key: 'http', zh: 'HTTP', en: 'HTTP' },
  { key: 'capture', zh: 'Tcpdump', en: 'Tcpdump' },
];

const RUN_HISTORY_STORAGE_KEY = 'ongrid-daily-tools-runs-v1';

type StoredRunHistory = {
  runs: ToolRun[];
  captureRuns: CaptureQuickRun[];
};

export default function DailyToolsPage() {
  const { tr } = useI18n();
  const [edges, setEdges] = useState<Edge[]>([]);
  const [edgesLoading, setEdgesLoading] = useState(true);
  const [edgesErr, setEdgesErr] = useState('');
  const [selectedEdgeIDs, setSelectedEdgeIDs] = useState<number[]>([]);
  const [edgeQuery, setEdgeQuery] = useState('');
  const [active, setActive] = useState<ToolKey>('ping');
  const [netnsOptions, setNetnsOptions] = useState<string[]>([]);
  const [netnsLoading, setNetnsLoading] = useState(false);
  const [netnsErr, setNetnsErr] = useState('');
  const [runs, setRuns] = useState<ToolRun[]>([]);
  const [selectedResult, setSelectedResult] = useState<ProbeResult | null>(null);
  const [captureRuns, setCaptureRuns] = useState<CaptureQuickRun[]>([]);
  const [nowMs, setNowMs] = useState(() => Date.now());
  const [historyHydrated, setHistoryHydrated] = useState(false);
  const pendingRestoredRunIDsRef = useRef(new Set<string>());
  const capturePollInFlightRef = useRef(new Set<string>());

  const [ping, setPing] = useState<PingForm>({ host: '', count: '4', timeout_ms: '3000' });
  const [dns, setDNS] = useState<DNSForm>({ host: '', timeout_ms: '3000' });
  const [tcp, setTCP] = useState<TCPForm>({ host: '', port: '443', timeout_ms: '3000' });
  const [http, setHTTP] = useState<HTTPForm>({ url: '', method: 'HEAD', timeout_ms: '5000', skip_tls: false });
  const [advanced, setAdvanced] = useState<ExecAdvancedForm>({ namespace: '' });
  const [capture, setCapture] = useState<CaptureForm>({
    interface_name: 'eth0',
    filter: '',
    duration_seconds: '60',
    max_bytes_mb: '64',
    max_packets: '100000',
    snaplen: '1514',
    promiscuous: false,
    title: '',
  });

  const loadEdges = useCallback(async () => {
    setEdgesLoading(true);
    try {
      const r = await listEdges();
      const items = r.items ?? [];
      setEdges(items);
      setEdgesErr('');
      setSelectedEdgeIDs((current) => {
        return current.filter((id) => items.some((edge) => edge.id === id));
      });
    } catch (err) {
      setEdgesErr(err instanceof ApiError ? err.message : (err as Error).message);
    } finally {
      setEdgesLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadEdges();
  }, [loadEdges]);

  useEffect(() => {
    const stored = readRunHistory();
    pendingRestoredRunIDsRef.current = new Set(stored.runs.filter((run) => run.status === 'running').map((run) => run.id));
    setRuns(stored.runs);
    setCaptureRuns(stored.captureRuns);
    setHistoryHydrated(true);
  }, []);

  useEffect(() => {
    if (!historyHydrated) return;
    persistRunHistory({ runs, captureRuns });
  }, [captureRuns, historyHydrated, runs]);

  useEffect(() => {
    const hasLiveWork = runs.some((run) => run.status === 'running') || captureRuns.some((run) => run.status === 'starting' || run.status === 'capturing' || run.status === 'stopping');
    if (!hasLiveWork) return undefined;
    const timer = window.setInterval(() => setNowMs(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [captureRuns, runs]);

  useEffect(() => {
    const liveCaptures = captureRuns.filter((run) => run.status === 'starting' || run.status === 'capturing' || run.status === 'stopping');
    if (liveCaptures.length === 0) return undefined;
    const timer = window.setInterval(() => {
      liveCaptures.forEach((run) => {
        void pollCaptureRun(run);
      });
    }, 1500);
    return () => window.clearInterval(timer);
  }, [captureRuns]);

  const selectedEdges = useMemo(() => {
    const selected = new Set(selectedEdgeIDs);
    return edges.filter((edge) => selected.has(edge.id));
  }, [edges, selectedEdgeIDs]);
  const visibleEdges = useMemo(() => {
    const q = edgeQuery.trim().toLowerCase();
    if (!q) return edges;
    return edges.filter((edge) => edgeSearchText(edge).includes(q));
  }, [edges, edgeQuery]);
  const running = runs.some((run) => run.status === 'running');
  const activeTool = TOOLS.find((tool) => tool.key === active) ?? TOOLS[0];
  const workspaceRuns = runs.slice(0, 6);

  useEffect(() => {
    if (selectedEdges.length === 0) {
      setAdvanced({ namespace: '' });
      setNetnsOptions([]);
      setNetnsErr('');
      setNetnsLoading(false);
      return undefined;
    }
    let activeRequest = true;
    setNetnsLoading(true);
    setNetnsErr('');
    void Promise.all(selectedEdges.map((edge) => listOperatorNetNS(edge.id)))
      .then((responses) => {
        if (!activeRequest) return;
        const namespacesByEdge = responses.map((resp) => new Set((resp.namespaces ?? []).map((item) => item.trim()).filter(Boolean)));
        const items = [...namespacesByEdge[0]].filter((item) => namespacesByEdge.every((namespaces) => namespaces.has(item))).sort();
        setNetnsOptions(items);
        setAdvanced((current) => items.includes(current.namespace) ? current : { namespace: '' });
      })
      .catch((err) => {
        if (!activeRequest) return;
        setNetnsOptions([]);
        setNetnsErr(err instanceof ApiError ? err.message : (err as Error).message);
      })
      .finally(() => {
        if (activeRequest) setNetnsLoading(false);
      });
    return () => {
      activeRequest = false;
    };
  }, [selectedEdges]);

  function patchRunResult(runID: string, edgeID: number, patch: Partial<ProbeResult>) {
    setRuns((prev) => prev.map((run) => run.id === runID ? {
      ...run,
      results: run.results.map((result) => result.edgeID === edgeID ? { ...result, ...patch } : result),
    } : run));
  }

  function appendRunLog(runID: string, event: Omit<RunLogEvent, 'id' | 'ts'>) {
    setRuns((prev) => prev.map((run) => run.id === runID ? { ...run, logs: [...run.logs, runLog(event)] } : run));
  }

  function appendCaptureLog(runID: string, event: Omit<RunLogEvent, 'id' | 'ts'>) {
    setCaptureRuns((prev) => prev.map((run) => run.id === runID ? { ...run, logs: [...run.logs, runLog(event)] } : run));
  }

  async function pollCaptureRun(run: CaptureQuickRun) {
    if (capturePollInFlightRef.current.has(run.id)) return;
    capturePollInFlightRef.current.add(run.id);
    try {
      if (run.sessionID) {
        const out = await refreshPacketCaptureSession(run.sessionID);
        applyCapturePoll(run, out.captures ?? [], sessionLink(out.session));
        return;
      }
      if (run.captureIDs.length === 0) return;
      const captures = await Promise.all(run.captureIDs.map((id) => getPacketCapture(id)));
      applyCapturePoll(run, captures, captureQuickLink(captures[0]));
    } catch (err) {
      appendCaptureLog(run.id, { stream: 'stderr', message: err instanceof ApiError ? err.message : (err as Error).message });
    } finally {
      capturePollInFlightRef.current.delete(run.id);
    }
  }

  function applyCapturePoll(run: CaptureQuickRun, captures: PacketCapture[], link: string) {
    const members = captures.map((captureItem, idx) => captureMemberFromCapture(captureItem, run.edgeLabels[idx]));
    const previous = new Map(run.members.map((member) => [member.id, member]));
    const stateChanges = members
      .filter((member) => previous.get(member.id) && previous.get(member.id)?.state !== member.state)
      .map((member) => runLog({
        stream: captureStateIsTerminal(member.state) ? (member.state === 'failed' ? 'stderr' : 'status') : 'status',
        edgeLabel: member.edgeLabel,
        message: `capture_id=${member.id} ${previous.get(member.id)?.state} -> ${member.state}`,
      }));
    const progressChanges = members
      .filter((member) => {
        const before = previous.get(member.id);
        return before?.state === 'capturing' && member.state === 'capturing'
          && (before.capturedPackets !== member.capturedPackets || before.capturedBytes !== member.capturedBytes);
      })
      .map((member) => runLog({
        stream: 'stdout',
        edgeLabel: member.edgeLabel,
        message: tr(`已采集 ${member.capturedPackets ?? 0} 个包，${formatBytes(member.capturedBytes ?? 0)}。`, `Captured ${member.capturedPackets ?? 0} packets, ${formatBytes(member.capturedBytes ?? 0)}.`),
      }));
    const previewChanges = members.flatMap((member) => {
      const lines = appendedPreviewLines(previous.get(member.id)?.livePreview ?? [], member.livePreview ?? []);
      return lines.map((message) => runLog({ stream: 'stdout', edgeLabel: member.edgeLabel, message }));
    });
    const hasFailed = members.some((member) => member.state === 'failed');
    const allDone = members.length > 0 && members.every((member) => captureStateIsTerminal(member.state));
    setCaptureRuns((prev) => prev.map((item) => {
      if (item.id !== run.id) return item;
      const nextStatus: CaptureQuickStatus = allDone
        ? members.every((member) => member.state === 'cancelled') ? 'cancelled' : hasFailed ? 'error' : 'ready'
        : item.status === 'stopping' ? 'stopping' : 'capturing';
      const nextLogs = stateChanges.length > 0 || progressChanges.length > 0 || previewChanges.length > 0 ? [...item.logs, ...stateChanges, ...progressChanges, ...previewChanges] : item.logs;
      const terminalLog = allDone && (item.status === 'capturing' || item.status === 'stopping')
        ? [runLog({ stream: hasFailed ? 'stderr' : 'status', message: hasFailed ? tr('抓包结束：存在失败成员。', 'Capture finished with failed member(s).') : tr('抓包已完成。', 'Capture completed.') })]
        : [];
      return {
        ...item,
        status: nextStatus,
        finishedAt: allDone ? item.finishedAt ?? new Date().toISOString() : item.finishedAt,
        captureIDs: members.map((member) => member.id),
        link,
        members,
        logs: [...nextLogs, ...terminalLog],
      };
    }));
  }

  async function runCurrent() {
    if (selectedEdges.length === 0) return;
    if (active === 'capture') {
      await startCapture(selectedEdges);
      return;
    }
    await runParallelProbe(active, active === 'ping' ? selectedEdges : selectedEdges);
  }

  async function runParallelProbe(tool: Exclude<ToolKey, 'capture'>, targets: Edge[]) {
    const target = toolTarget(tool, { ping, dns, tcp, http });
    try {
      const out = await createOperatorRun({
        edge_ids: targets.map((edge) => edge.id),
        command: operatorCommand(tool),
        args: toolParams(tool, { ping, dns, tcp, http }, advanced),
        timeout_ms: toolTimeoutMs(tool, { ping, dns, tcp, http }),
      });
      const run = {
        ...toolRunFromOperator(out, tool, target, targets, tr),
        logs: [runLog({ stream: 'system', message: `$ ${toolCommand(tool, { ping, dns, tcp, http }, advanced)}` })],
      };
      setRuns((prev) => [run, ...prev]);
      setSelectedResult(null);
      void streamOperatorRunEvents(out.id, (event) => applyOperatorEvent(out.id, event, targets));
    } catch (err) {
      const runID = crypto.randomUUID();
      const message = err instanceof ApiError ? err.message : (err as Error).message;
      setRuns((prev) => [{
        id: runID,
        tool,
        target,
        title: `${toolLabel(tool, tr)} ${target}`,
        status: 'error',
        startedAt: new Date().toISOString(),
        finishedAt: new Date().toISOString(),
        results: targets.map((edge) => ({ edgeID: edge.id, edgeLabel: edgeLabel(edge), status: 'error', error: message })),
        logs: [runLog({ stream: 'stderr', message })],
      }, ...prev]);
    }
  }

  function applyOperatorEvent(runID: string, event: OperatorRunEvent, targets: Edge[]) {
    if (event.type === 'edge_running' && event.edge_id) {
      patchRunResult(runID, event.edge_id, { status: 'running', startedAt: event.ts });
    }
    if ((event.type === 'stdout' || event.type === 'stderr') && event.edge_id) {
      appendRunLog(runID, { stream: event.type === 'stderr' ? 'stderr' : 'stdout', edgeLabel: edgeLabelByID(targets, event.edge_id), message: event.message ?? '' });
      if (event.type === 'stdout') {
        patchRunResult(runID, event.edge_id, { result: { stdout: event.message ?? '' } });
      } else {
        patchRunResult(runID, event.edge_id, { error: event.message ?? '' });
      }
    }
    if (event.type === 'edge_done' && event.edge_id) {
      patchRunResult(runID, event.edge_id, {
        status: event.status === 'success' ? 'success' : event.status === 'cancelled' ? 'error' : 'error',
        finishedAt: event.ts,
        durationMs: event.duration_ms,
        error: event.status === 'success' ? undefined : event.message,
      });
      appendRunLog(runID, { stream: event.status === 'success' ? 'status' : 'stderr', edgeLabel: edgeLabelByID(targets, event.edge_id), message: event.message || tr('执行完成。', 'Completed.') });
    }
    if (event.type === 'created' && event.message) {
      appendRunLog(runID, { stream: 'system', message: event.message });
    }
    if (event.type === 'done') {
      setRuns((prev) => prev.map((item) => item.id === runID ? {
        ...item,
        status: event.status === 'success' ? 'success' : event.status === 'partial' ? 'partial' : 'error',
        finishedAt: event.ts,
        logs: [...item.logs, runLog({ stream: event.status === 'success' ? 'status' : 'stderr', message: event.message || tr('运行结束。', 'Run finished.') })],
      } : item));
    }
  }

  useEffect(() => {
    if (!historyHydrated || edges.length === 0) return;
    const controllers: AbortController[] = [];
    runs.filter((run) => run.status === 'running' && pendingRestoredRunIDsRef.current.has(run.id)).forEach((run) => {
      pendingRestoredRunIDsRef.current.delete(run.id);
      const targets = run.results.map((result) => edges.find((edge) => edge.id === result.edgeID) ?? {
        id: result.edgeID,
        name: result.edgeLabel,
        status: 'unknown' as Edge['status'],
        roles: [],
        access_key_id: '',
        last_seen_at: null,
      });
      const controller = new AbortController();
      controllers.push(controller);
      void getOperatorRun(run.id)
        .then((remote) => {
          if (remote.status === 'running') return streamOperatorRunEvents(run.id, (event) => applyOperatorEvent(run.id, event, targets), controller.signal);
          applyOperatorEvent(run.id, {
            id: `restore-${run.id}`,
            type: 'done',
            ts: remote.finished_at ?? new Date().toISOString(),
            run_id: run.id,
            status: remote.status,
            message: remote.status === 'success' ? tr('已恢复已完成运行。', 'Restored completed run.') : tr('已恢复已结束运行。', 'Restored finished run.'),
          }, targets);
          return undefined;
        })
        .catch((err) => {
          appendRunLog(run.id, { stream: 'stderr', message: err instanceof ApiError ? err.message : (err as Error).message });
        });
    });
    return () => controllers.forEach((controller) => controller.abort());
  }, [edges, historyHydrated, runs, tr]);

  async function startCapture(targets: Edge[]) {
    const eligible = targets.filter((edge) => edge.device_id);
    const quickID = crypto.randomUUID();
    const title = capture.title.trim() || tr('日常工具抓包', 'Daily tools capture');
    const quickRun: CaptureQuickRun = {
      id: quickID,
      status: eligible.length === 0 ? 'error' : 'starting',
      title,
      target: capture.filter.trim() || tr('全部流量', 'all traffic'),
      edgeLabels: targets.map(edgeLabel),
      startedAt: new Date().toISOString(),
      captureIDs: [],
      link: '/pages?tab=packets',
      members: [],
      logs: [
        runLog({ stream: 'system', message: tr(`准备在 ${eligible.length} 台 Edge 上发起抓包。`, `Preparing capture on ${eligible.length} Edge(s).`) }),
        runLog({ stream: 'system', message: `$ ${captureCommand(capture, advanced)}` }),
      ],
      error: eligible.length === 0 ? tr('选中的 Edge 没有关联 device_id。', 'Selected Edges have no linked device_id.') : undefined,
    };
    setCaptureRuns((prev) => [quickRun, ...prev]);
    if (eligible.length === 0) return;

    try {
      eligible.forEach((edge) => appendCaptureLog(quickID, { stream: 'status', edgeLabel: edgeLabel(edge), message: tr('已加入抓包会话。', 'Added to capture session.') }));
      const out = await createPacketCaptureSession({
        targets: eligible.map((edge) => ({ device_id: edge.device_id as number, interface: capture.interface_name.trim() || 'eth0', network_namespace: advanced.namespace.trim() || undefined })),
        filter: capture.filter.trim(),
        duration_seconds: numberOrDefault(capture.duration_seconds, 60),
        max_bytes: numberOrDefault(capture.max_bytes_mb, 64) * 1024 * 1024,
        max_packets: numberOrDefault(capture.max_packets, 100000),
        snaplen: numberOrDefault(capture.snaplen, 1514),
        promiscuous: capture.promiscuous,
        title,
        description: 'Created from Daily Tools.',
      });
      const memberErrorText = out.member_errors?.join('; ');
      const hasMembers = out.captures.length > 0;
      setCaptureRuns((prev) => prev.map((item) => item.id === quickID ? {
        ...item,
        status: hasMembers ? 'capturing' : 'error',
        sessionID: out.session.id,
        captureIDs: out.captures.map((member) => member.id),
        link: sessionLink(out.session),
        members: out.captures.map((member, idx) => captureMemberFromCapture(member, eligible[idx] ? edgeLabel(eligible[idx]) : undefined)),
        error: memberErrorText,
      } : item));
      if (hasMembers) {
        appendCaptureLog(quickID, { stream: 'status', message: tr(`抓包会话已开始，${out.captures.length} 个成员任务。`, `Capture session started with ${out.captures.length} member task(s).`) });
      } else {
        appendCaptureLog(quickID, { stream: 'stderr', message: memberErrorText || tr('抓包没有创建任何成员任务。', 'Capture created no member tasks.') });
      }
      if (memberErrorText && hasMembers) appendCaptureLog(quickID, { stream: 'stderr', message: memberErrorText });
    } catch (err) {
      appendCaptureLog(quickID, { stream: 'stderr', message: err instanceof ApiError ? err.message : (err as Error).message });
      setCaptureRuns((prev) => prev.map((item) => item.id === quickID ? {
        ...item,
        status: 'error',
        error: err instanceof ApiError ? err.message : (err as Error).message,
      } : item));
    }
  }

  async function stopCapture(run: CaptureQuickRun) {
    if (run.captureIDs.length === 0) return;
    appendCaptureLog(run.id, { stream: 'system', message: tr(`停止并保存 ${run.captureIDs.length} 个抓包成员任务。`, `Stopping and saving ${run.captureIDs.length} capture member task(s).`) });
    setCaptureRuns((prev) => prev.map((item) => item.id === run.id ? { ...item, status: 'stopping' } : item));
    try {
      if (run.sessionID) {
        await stopPacketCaptureSession(run.sessionID);
      } else {
        await Promise.all(run.captureIDs.map((id) => stopPacketCapture(id)));
      }
    } catch (err) {
      appendCaptureLog(run.id, { stream: 'stderr', message: err instanceof ApiError ? err.message : (err as Error).message });
      setCaptureRuns((prev) => prev.map((item) => item.id === run.id ? { ...item, status: 'capturing' } : item));
      return;
    }
    setCaptureRuns((prev) => prev.map((item) => item.id === run.id ? {
      ...item,
      status: 'stopping',
    } : item));
    appendCaptureLog(run.id, { stream: 'status', message: tr('已发送停止并保存请求，正在上传已有数据包。', 'Stop and save request sent; uploading collected packets.') });
  }

  async function discardCapture(run: CaptureQuickRun) {
    if (run.captureIDs.length === 0) return;
    appendCaptureLog(run.id, { stream: 'system', message: tr(`取消并丢弃 ${run.captureIDs.length} 个抓包成员任务。`, `Cancelling and discarding ${run.captureIDs.length} capture member task(s).`) });
    try {
      await Promise.all(run.captureIDs.map((id) => cancelPacketCapture(id)));
    } catch (err) {
      appendCaptureLog(run.id, { stream: 'stderr', message: err instanceof ApiError ? err.message : (err as Error).message });
      return;
    }
    const finishedAt = new Date().toISOString();
    setCaptureRuns((prev) => prev.map((item) => item.id === run.id ? {
      ...item,
      status: 'cancelled',
      finishedAt,
      members: item.members.map((member) => captureStateIsTerminal(member.state) ? member : { ...member, state: 'cancelled', finishedAt }),
    } : item));
    appendCaptureLog(run.id, { stream: 'status', message: tr('已取消任务，采集数据将不会上传。', 'Task cancelled; collected packets will not be uploaded.') });
  }

  async function cancelToolRun(run: ToolRun) {
    if (run.status !== 'running') return;
    appendRunLog(run.id, { stream: 'system', message: tr('发送停止请求。', 'Sending stop request.') });
    try {
      await cancelOperatorRun(run.id);
    } catch (err) {
      appendRunLog(run.id, { stream: 'stderr', message: err instanceof ApiError ? err.message : (err as Error).message });
    }
  }

  const canRun = selectedEdges.length > 0 && toolInputReady(active, { ping, dns, tcp, http, capture });
  return (
    <main className="anim-fade flex h-full min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={tr('工具', 'Tools')}
        subtitle={tr('面向值班排障的交互式工具台；结果保存在当前浏览器。', 'Interactive operations workbench; results stay in this browser.')}
        extra={(
          <form onSubmit={(event) => { event.preventDefault(); void runCurrent(); }} className="flex flex-col gap-2">
            <div className="flex flex-wrap items-end gap-2">
              <EdgePicker
                edges={visibleEdges}
                allEdges={edges}
                selectedIDs={selectedEdgeIDs}
                query={edgeQuery}
                loading={edgesLoading}
                onQuery={setEdgeQuery}
                onToggle={(id) => setSelectedEdgeIDs((current) => current.includes(id) ? current.filter((item) => item !== id) : [...current, id])}
                onClear={() => setSelectedEdgeIDs([])}
              />
              <AdvancedFields
                value={advanced}
                options={netnsOptions}
                loading={netnsLoading}
                disabled={selectedEdges.length === 0}
                onChange={setAdvanced}
              />
              <label className="block w-40 shrink-0">
                <span className="mb-1 block text-[11px] text-zinc-500">{tr('工具', 'Tool')}</span>
                <select value={active} onChange={(event) => setActive(event.target.value as ToolKey)} className={inputClassName}>
                  {TOOLS.map((item) => <option key={item.key} value={item.key}>{tr(item.zh, item.en)}</option>)}
                </select>
              </label>
              <div className="contents">
                {active === 'ping' && <PingFields value={ping} onChange={setPing} />}
                {active === 'dns' && <DNSFields value={dns} onChange={setDNS} />}
                {active === 'tcp' && <TCPFields value={tcp} onChange={setTCP} />}
                {active === 'http' && <HTTPFields value={http} onChange={setHTTP} />}
                {active === 'capture' && <CaptureFields value={capture} selectedEdges={selectedEdges} onChange={setCapture} />}
              </div>
              <button
                type="submit"
                disabled={!canRun}
                className="ml-auto inline-flex h-9 shrink-0 items-center gap-1.5 self-end rounded-md bg-accent px-3 text-xs font-medium text-accent-fg hover:bg-accent/90 disabled:opacity-50"
              >
                <Play size={12} fill="currentColor" />
                {active === 'capture' ? tr('开始', 'Start') : tr('执行', 'Run')}
              </button>
            </div>
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              <span className="text-xs text-zinc-500">{tr(`${selectedEdges.length} 台 Edge · ${activeTool.zh}`, `${selectedEdges.length} Edge(s) · ${activeTool.en}`)}</span>
              <span className="text-xs text-zinc-500">{active === 'capture' ? tr('抓包会短时读取目标 Edge 网卡流量，详情在数据包页。', 'Capture briefly reads target Edge interface traffic; details are in packet pages.') : tr('按已勾选 Edge 并行执行。', 'Runs across checked Edges.')}</span>
              {edgesErr ? <span className="text-xs text-red-300">{edgesErr}</span> : null}
              {netnsErr ? <span className="text-xs text-amber-300">{tr('命名空间获取失败：', 'Failed to load namespaces: ')}{netnsErr}</span> : null}
              {visibleEdges.length === 0 ? <span className="text-xs text-zinc-500">{tr('没有匹配的 Edge', 'No matching Edges')}</span> : null}
            </div>
          </form>
        )}
      />
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-6 py-5">
        <div className="space-y-4">
          {(runs.length > 0 || captureRuns.length > 0) ? <div className="flex justify-end"><Button onClick={() => { setRuns([]); setCaptureRuns([]); setSelectedResult(null); }}>{tr('清空运行', 'Clear runs')}</Button></div> : null}
          {captureRuns.length > 0 ? <CaptureQuickPanel runs={captureRuns} nowMs={nowMs} onStop={(run) => void stopCapture(run)} onDiscard={(run) => void discardCapture(run)} /> : null}
          <section className="min-h-[420px]">
            {runs.length === 0 ? (
              <div className="py-20"><EmptyState icon={Activity} title={tr('尚未执行工具', 'No tool runs yet')} hint={tr('选择 Edge 和工具后执行；多次执行会在这里分栏或分页。', 'Select Edges and a tool to run; repeated runs appear here for comparison.')} /></div>
            ) : (
              <div className={cn('grid grid-cols-1 gap-4', workspaceRuns.length > 1 && '2xl:grid-cols-2')}>
                {workspaceRuns.map((run) => <RunPanel key={run.id} run={run} nowMs={nowMs} onInspect={setSelectedResult} onCancel={(item) => void cancelToolRun(item)} onClose={(id) => closeRun(id, setRuns, setSelectedResult)} />)}
              </div>
            )}
          </section>
        </div>
      </div>
      {selectedResult ? <ResultDrawer result={selectedResult} onClose={() => setSelectedResult(null)} /> : null}
    </main>
  );
}

function readRunHistory(): StoredRunHistory {
  try {
    const raw = window.localStorage.getItem(RUN_HISTORY_STORAGE_KEY);
    if (!raw) return { runs: [], captureRuns: [] };
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object') return { runs: [], captureRuns: [] };
    const value = parsed as Partial<StoredRunHistory>;
    return {
      runs: Array.isArray(value.runs) ? value.runs : [],
      captureRuns: Array.isArray(value.captureRuns) ? value.captureRuns : [],
    };
  } catch {
    return { runs: [], captureRuns: [] };
  }
}

function persistRunHistory(history: StoredRunHistory) {
  try {
    window.localStorage.setItem(RUN_HISTORY_STORAGE_KEY, JSON.stringify(history));
  } catch {
    // Browser storage may be unavailable or full; the current page remains usable.
  }
}

function closeRun(id: string, setRuns: React.Dispatch<React.SetStateAction<ToolRun[]>>, setSelectedResult: React.Dispatch<React.SetStateAction<ProbeResult | null>>) {
  setRuns((prev) => prev.filter((run) => run.id !== id));
  setSelectedResult(null);
}

function SectionTitle({ title, hint }: { title: string; hint?: string }) {
  return (
    <div>
      <h2 className="text-sm font-medium text-zinc-100">{title}</h2>
      {hint ? <p className="mt-0.5 text-xs text-zinc-500">{hint}</p> : null}
    </div>
  );
}

function PingFields({ value, onChange }: { value: PingForm; onChange(value: PingForm): void }) {
  const { tr } = useI18n();
  return (
    <FieldGrid>
      <TextField label={tr('目标 Host / IP', 'Target host / IP')} value={value.host} onChange={(host) => onChange({ ...value, host })} placeholder="8.8.8.8" />
      <NumberField label={tr('包数', 'Count')} value={value.count} onChange={(count) => onChange({ ...value, count })} min={1} max={10} />
      <NumberField label={tr('超时（毫秒）', 'Timeout (ms)')} value={value.timeout_ms} onChange={(timeout_ms) => onChange({ ...value, timeout_ms })} min={100} max={10000} />
    </FieldGrid>
  );
}

function DNSFields({ value, onChange }: { value: DNSForm; onChange(value: DNSForm): void }) {
  const { tr } = useI18n();
  return (
    <FieldGrid>
      <TextField label={tr('域名', 'Domain')} value={value.host} onChange={(host) => onChange({ ...value, host })} placeholder="example.com" />
      <NumberField label={tr('超时（毫秒）', 'Timeout (ms)')} value={value.timeout_ms} onChange={(timeout_ms) => onChange({ ...value, timeout_ms })} min={100} max={10000} />
      <Hint>{tr('当前返回 A/AAAA 解析结果。', 'Currently returns A/AAAA lookup results.')}</Hint>
    </FieldGrid>
  );
}

function TCPFields({ value, onChange }: { value: TCPForm; onChange(value: TCPForm): void }) {
  const { tr } = useI18n();
  return (
    <FieldGrid>
      <TextField label={tr('目标 Host / IP', 'Target host / IP')} value={value.host} onChange={(host) => onChange({ ...value, host })} placeholder="101.34.63.91" />
      <NumberField label={tr('端口', 'Port')} value={value.port} onChange={(port) => onChange({ ...value, port })} min={1} max={65535} />
      <NumberField label={tr('超时（毫秒）', 'Timeout (ms)')} value={value.timeout_ms} onChange={(timeout_ms) => onChange({ ...value, timeout_ms })} min={100} max={10000} />
    </FieldGrid>
  );
}

function HTTPFields({ value, onChange }: { value: HTTPForm; onChange(value: HTTPForm): void }) {
  const { tr } = useI18n();
  return (
    <FieldGrid>
      <TextField className="w-96" label="URL" value={value.url} onChange={(url) => onChange({ ...value, url })} placeholder="https://example.com/health" />
      <label className="w-28 shrink-0 space-y-1">
        <span className="text-xs text-zinc-400">{tr('方法', 'Method')}</span>
        <select value={value.method} onChange={(event) => onChange({ ...value, method: event.target.value as HTTPForm['method'] })} className={inputClassName}>
          <option value="HEAD">HEAD</option>
          <option value="GET">GET</option>
        </select>
      </label>
      <NumberField label={tr('超时（毫秒）', 'Timeout (ms)')} value={value.timeout_ms} onChange={(timeout_ms) => onChange({ ...value, timeout_ms })} min={100} max={30000} />
      <label className="flex h-9 shrink-0 items-center gap-2 rounded-md border border-zinc-800 bg-zinc-950 px-2.5 text-[12px] text-zinc-300">
        <input type="checkbox" checked={value.skip_tls} onChange={(event) => onChange({ ...value, skip_tls: event.target.checked })} className="h-4 w-4 accent-indigo-600" />
        {tr('跳过 TLS 检测', 'Skip TLS verify')}
      </label>
    </FieldGrid>
  );
}

function AdvancedFields({
  value,
  options,
  loading,
  disabled,
  onChange,
}: {
  value: ExecAdvancedForm;
  options: string[];
  loading: boolean;
  disabled: boolean;
  onChange(value: ExecAdvancedForm): void;
}) {
  const { tr } = useI18n();
  return (
    <label className="w-48 shrink-0 space-y-1">
      <span className="block text-[11px] leading-4 text-zinc-400">{tr('网络命名空间', 'Net namespace')}</span>
      <select
        value={value.namespace}
        disabled={disabled || loading}
        onChange={(event) => onChange({ ...value, namespace: event.target.value })}
        className={cn(inputClassName, (disabled || loading) && 'text-zinc-500')}
      >
        <option value="">{loading ? tr('加载中...', 'Loading...') : tr('Host 网络', 'Host network')}</option>
        {options.map((item) => <option key={item} value={item}>{item}</option>)}
      </select>
    </label>
  );
}

function EdgePicker({
  edges,
  allEdges,
  selectedIDs,
  query,
  loading,
  onQuery,
  onToggle,
  onClear,
}: {
  edges: Edge[];
  allEdges: Edge[];
  selectedIDs: number[];
  query: string;
  loading: boolean;
  onQuery(value: string): void;
  onToggle(id: number): void;
  onClear(): void;
}) {
  const { tr } = useI18n();
  const [open, setOpen] = useState(false);
  const boxRef = useRef<HTMLDivElement | null>(null);
  const selected = new Set(selectedIDs);
  const selectedEdges = allEdges.filter((edge) => selected.has(edge.id));
  const label = selectedEdges.length === 0
    ? tr('选择 Edge', 'Select Edge')
    : selectedEdges.length === 1
      ? edgeLabel(selectedEdges[0])
      : tr(`${selectedEdges.length} 台 Edge`, `${selectedEdges.length} Edges`);
  useEffect(() => {
    if (!open) return undefined;
    const onPointerDown = (event: PointerEvent) => {
      if (!boxRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    document.addEventListener('pointerdown', onPointerDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('pointerdown', onPointerDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);
  return (
    <div ref={boxRef} className="relative w-80 min-w-[240px] max-w-full shrink-0 space-y-1">
      <span className="block text-[11px] leading-4 text-zinc-400">{tr('执行目标', 'Execution target')}</span>
      <button
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        className={cn(inputClassName, 'flex items-center justify-between gap-2 text-left')}
      >
        <span className={cn('min-w-0 truncate', selectedEdges.length === 0 ? 'text-zinc-500' : 'text-zinc-100')}>{label}</span>
        <ChevronDown size={14} className="shrink-0 text-zinc-500" />
      </button>
      {open ? (
        <div className="absolute left-0 top-full z-30 mt-1 w-full overflow-hidden rounded-md border border-zinc-800 bg-zinc-950 shadow-xl">
          <div className="border-b border-zinc-800/60 bg-zinc-900/30 p-2">
            <div className="relative">
              <Search size={13} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-zinc-500" />
              <input
                value={query}
                onChange={(event) => onQuery(event.target.value)}
                aria-label={tr('搜索 Edge', 'Search Edge')}
                placeholder={tr('按名称、主机名或 ID 筛选', 'Filter by name, host, or ID')}
                className={cn(inputClassName, 'h-8 bg-zinc-950/70 pr-8 pl-8 text-[11px]')}
                autoFocus
              />
              {query ? (
                <button
                  type="button"
                  onClick={() => onQuery('')}
                  aria-label={tr('清空搜索', 'Clear search')}
                  className="absolute right-1.5 top-1/2 inline-flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded text-zinc-500 hover:bg-zinc-900 hover:text-zinc-200"
                >
                  <X size={13} />
                </button>
              ) : null}
            </div>
            {selectedIDs.length > 0 ? (
              <button
                type="button"
                onClick={onClear}
                className="mt-2 text-[11px] text-zinc-500 hover:text-zinc-300"
              >
                {tr('清空已选', 'Clear selected')}
              </button>
            ) : null}
          </div>
          <div role="listbox" className="max-h-56 overflow-auto">
            {loading ? (
              <div className="flex items-center gap-2 px-3 py-2 text-[12px] text-zinc-500"><Loader2 size={12} className="animate-spin" />{tr('加载 Edge...', 'Loading Edges...')}</div>
            ) : edges.length === 0 ? (
              <div className="px-3 py-2 text-[12px] text-zinc-500">{tr('没有可选 Edge', 'No Edge available')}</div>
            ) : (
              <div className="divide-y divide-zinc-800/60">
                {edges.map((edge) => (
                  <label key={edge.id} className="flex cursor-pointer items-center gap-2 px-3 py-2 text-[12px] hover:bg-zinc-900/60">
                    <input
                      type="checkbox"
                      checked={selected.has(edge.id)}
                      onChange={() => onToggle(edge.id)}
                      aria-label={tr(`选择 ${edgeLabel(edge)}`, `Select ${edgeLabel(edge)}`)}
                      className="h-4 w-4 accent-indigo-600"
                    />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-zinc-200">{edgeLabel(edge)}</span>
                      <span className="block truncate text-[11px] text-zinc-500">{edgeMetaLine(edge)}</span>
                    </span>
                    <EdgeStatus edge={edge} />
                  </label>
                ))}
              </div>
            )}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function CaptureFields({ value, selectedEdges, onChange }: { value: CaptureForm; selectedEdges: Edge[]; onChange(value: CaptureForm): void }) {
  const { tr } = useI18n();
  const missing = selectedEdges.filter((edge) => !edge.device_id).length;
  return (
    <>
      <FieldGrid>
        <div className="flex shrink-0 items-end gap-2">
          <TextField className="w-28" label={tr('网卡', 'Interface')} value={value.interface_name} onChange={(interface_name) => onChange({ ...value, interface_name })} placeholder="eth0" />
          <label className="flex h-9 items-center gap-2 rounded-md border border-zinc-800 bg-zinc-950 px-2.5 text-[12px] text-zinc-300">
            <input type="checkbox" checked={value.promiscuous} onChange={(event) => onChange({ ...value, promiscuous: event.target.checked })} className="h-4 w-4 accent-indigo-600" />
            {tr('混杂模式', 'Promiscuous mode')}
          </label>
        </div>
        <NumberField className="w-28" label={tr('时长（秒）', 'Duration (sec)')} value={value.duration_seconds} onChange={(duration_seconds) => onChange({ ...value, duration_seconds })} min={1} max={300} />
        <TextField className="w-64" label="BPF filter" value={value.filter} onChange={(filter) => onChange({ ...value, filter })} placeholder="tcp port 443" />
        <NumberField className="w-28" label={tr('最大大小', 'Max size')} value={value.max_bytes_mb} onChange={(max_bytes_mb) => onChange({ ...value, max_bytes_mb })} min={1} max={256} />
        <NumberField className="w-28" label={tr('最大包数', 'Max packets')} value={value.max_packets} onChange={(max_packets) => onChange({ ...value, max_packets })} min={1} max={500000} />
        <NumberField className="w-28" label="Snaplen" value={value.snaplen} onChange={(snaplen) => onChange({ ...value, snaplen })} min={64} max={65535} />
        <TextField className="w-48" label={tr('标题', 'Title')} value={value.title} onChange={(title) => onChange({ ...value, title })} placeholder={tr('日常工具抓包', 'Daily tools capture')} />
      </FieldGrid>
      {missing > 0 ? <span className="self-end pb-2 text-xs text-red-300">{tr(`${missing} 台 Edge 未关联 device_id，会被跳过。`, `${missing} Edges have no linked device_id and will be skipped.`)}</span> : null}
    </>
  );
}

function RunPanel({ run, nowMs, onInspect, onCancel, onClose }: { run: ToolRun; nowMs: number; onInspect(result: ProbeResult): void; onCancel(run: ToolRun): void; onClose(id: string): void }) {
  const { tr } = useI18n();
  const ok = run.results.filter((result) => result.status === 'success').length;
  const failed = run.results.filter((result) => result.status === 'error').length;
  const done = ok + failed;
  return (
    <div className="min-w-0 rounded-xl border border-zinc-800/60 bg-zinc-950/40">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-zinc-800/60 px-4 py-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h3 className="truncate text-[13px] font-medium leading-5 text-zinc-100">{run.title}</h3>
            <RunStatusChip status={run.status} />
          </div>
          <p className="mt-0.5 text-[11px] text-zinc-500">{tr(`${done}/${run.results.length} 完成 · 成功 ${ok} · 失败 ${failed} · ${elapsedLabel(run.startedAt, run.finishedAt, nowMs)}`, `${done}/${run.results.length} done · ${ok} ok · ${failed} failed · ${elapsedLabel(run.startedAt, run.finishedAt, nowMs)}`)}</p>
        </div>
        <div className="flex items-center gap-2">
          {run.status === 'running' ? <Button onClick={() => onCancel(run)}><Square size={13} />{tr('停止', 'Stop')}</Button> : null}
          <Button onClick={() => void copyText(formatRun(run))}><Copy size={13} />{tr('复制结果', 'Copy')}</Button>
          <button type="button" onClick={() => onClose(run.id)} aria-label={tr('关闭', 'Close')} className="inline-flex h-7 w-7 items-center justify-center rounded-md border border-zinc-800 text-zinc-500 hover:bg-zinc-900 hover:text-zinc-200">
            <X size={13} />
          </button>
        </div>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[680px] text-left text-[11px]">
          <thead className="bg-zinc-900/60 text-zinc-500">
            <tr>
              <th className="px-4 py-2 font-medium">Edge</th>
              <th className="px-3 py-2 font-medium">{tr('状态', 'Status')}</th>
              <th className="px-3 py-2 font-medium">{tr('丢包率', 'Loss')}</th>
              <th className="px-3 py-2 font-medium">Avg</th>
              <th className="px-3 py-2 font-medium">P95</th>
              <th className="px-3 py-2 font-medium">{tr('耗时', 'Duration')}</th>
              <th className="px-4 py-2 text-right font-medium">{tr('详情', 'Detail')}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-zinc-800/60">
            {run.results.map((result) => {
              const metrics = pingMetrics(result.result);
              return (
                <tr key={result.edgeID} className="hover:bg-zinc-900/40">
                  <td className="px-4 py-2.5 text-zinc-200">{result.edgeLabel}</td>
                  <td className="px-3 py-2.5"><ProbeStatusChip status={result.status} /></td>
                  <td className="px-3 py-2.5 font-mono text-zinc-300">{metrics.loss ?? '-'}</td>
                  <td className="px-3 py-2.5 font-mono text-zinc-300">{metrics.avg ?? '-'}</td>
                  <td className="px-3 py-2.5 font-mono text-zinc-300">{metrics.p95 ?? '-'}</td>
                  <td className="px-3 py-2.5 font-mono text-zinc-300">{resultDuration(result, nowMs)}</td>
                  <td className="px-4 py-2.5 text-right"><Button onClick={() => onInspect(result)}><PanelRightOpen size={13} />{tr('查看', 'View')}</Button></td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <div className="border-t border-zinc-800/60 p-3">
        <RunConsole title={tr('执行日志', 'Run console')} status={run.status} logs={run.logs} />
      </div>
    </div>
  );
}

function CaptureQuickPanel({ runs, nowMs, onStop, onDiscard }: { runs: CaptureQuickRun[]; nowMs: number; onStop(run: CaptureQuickRun): void; onDiscard(run: CaptureQuickRun): void }) {
  const { tr } = useI18n();
  return (
    <Card compact>
      <SectionTitle title={tr('抓包快捷任务', 'Capture quick tasks')} hint={tr('临时状态；完整内容在数据包页。', 'Temporary status; full details are in packet pages.')} />
      <div className="mt-2 divide-y divide-zinc-800/60">
        {runs.map((run) => (
          <div key={run.id} className="flex flex-wrap items-center gap-3 py-3">
            <CaptureStatusIcon status={run.status} />
              <div className="min-w-0 flex-1">
                <div className="truncate text-[13px] leading-5 text-zinc-100">{run.title}</div>
                <div className="mt-0.5 truncate text-[11px] leading-4 text-zinc-500">{run.edgeLabels.join(', ')} · {run.target} · {elapsedLabel(run.startedAt, run.finishedAt, nowMs)}</div>
                {run.error ? <div className="mt-1 text-xs text-red-300">{run.error}</div> : null}
              </div>
            {(run.status === 'capturing' || run.status === 'starting') ? <>
              <Button onClick={() => onStop(run)} disabled={run.captureIDs.length === 0}><Square size={13} />{tr('停止并保存', 'Stop & save')}</Button>
              <Button variant="danger" onClick={() => onDiscard(run)} disabled={run.captureIDs.length === 0}><X size={13} />{tr('取消并丢弃', 'Cancel & discard')}</Button>
            </> : null}
            {run.status === 'stopping' ? <span className="inline-flex items-center gap-1.5 text-xs text-sky-400"><Loader2 size={13} className="animate-spin" />{tr('正在停止并保存', 'Stopping and saving')}</span> : null}
            <Link to={run.link} className="inline-flex items-center gap-1.5 rounded-md border border-zinc-700 bg-zinc-900 px-2.5 py-1.5 text-xs font-medium text-zinc-300 hover:bg-zinc-800"><FileCode2 size={13} />{tr('打开数据包', 'Open packets')}</Link>
            {run.members.length > 0 ? (
              <div className="basis-full overflow-hidden rounded-lg border border-zinc-800/60">
                <div className="grid grid-cols-[minmax(0,1fr)_86px_86px_86px] gap-2 bg-zinc-900/50 px-3 py-2 text-[11px] text-zinc-500">
                  <span>Edge</span>
                  <span>{tr('状态', 'State')}</span>
                  <span>{tr('包数', 'Packets')}</span>
                  <span>{tr('大小', 'Size')}</span>
                </div>
                <div className="divide-y divide-zinc-800/60">
                  {run.members.map((member) => (
                    <div key={member.id} className="grid grid-cols-[minmax(0,1fr)_86px_86px_86px] gap-2 px-3 py-2 text-[11px]">
                      <span className="min-w-0 truncate text-zinc-300">{member.edgeLabel} · capture {member.id}</span>
                      <span><CaptureMemberStateChip state={member.state} /></span>
                      <span className="font-mono text-zinc-300">{member.capturedPackets ?? '-'}</span>
                      <span className="font-mono text-zinc-300">{member.capturedBytes !== undefined ? formatBytes(member.capturedBytes) : '-'}</span>
                    </div>
                  ))}
                </div>
              </div>
            ) : null}
            <div className="basis-full">
              <RunConsole title={tr('执行日志', 'Run console')} status={captureRunStatus(run.status)} logs={run.logs} compact />
            </div>
          </div>
        ))}
      </div>
    </Card>
  );
}

function RunConsole({ title, status, logs, compact }: { title: string; status: RunStatus; logs: RunLogEvent[]; compact?: boolean }) {
  const { tr } = useI18n();
  const visibleLogs = logs.slice(-80);
  const termRef = useRef<XTerminalApi | null>(null);
  const lastRenderedRef = useRef('');
  const terminalText = useMemo(() => visibleLogs.length === 0
    ? `${ansiDim(tr('等待输出...', 'Waiting for output...'))}\r\n`
    : formatLogsAnsi(visibleLogs), [tr, visibleLogs]);
  const attachTerm = useCallback((api: XTerminalApi) => {
    termRef.current = api;
    lastRenderedRef.current = '';
    api.fit();
    api.clear();
    api.write(terminalText);
    lastRenderedRef.current = terminalText;
    window.setTimeout(() => {
      api.fit();
      api.clear();
      api.write(terminalText);
      lastRenderedRef.current = terminalText;
    }, 0);
  }, [terminalText]);
  useEffect(() => {
    const term = termRef.current;
    if (!term || lastRenderedRef.current === terminalText) return;
    term.fit();
    term.clear();
    term.write(terminalText);
    lastRenderedRef.current = terminalText;
  }, [terminalText]);
  return (
    <div className="overflow-hidden rounded-lg border border-zinc-800/60 bg-zinc-950">
      <div className="flex items-center justify-between gap-3 border-b border-zinc-800/60 bg-zinc-900/40 px-3 py-2">
        <div className="flex min-w-0 items-center gap-2">
          <TerminalSquare size={14} className="text-zinc-500" />
          <span className="truncate text-[12px] font-medium text-zinc-300">{title}</span>
          <RunStatusChip status={status} />
        </div>
        <Button onClick={() => void copyText(formatLogs(logs))}><Copy size={13} />{tr('复制', 'Copy')}</Button>
      </div>
      <div className={cn('bg-zinc-950 p-2', compact ? 'h-44' : 'h-64')}>
        <pre className="sr-only">{formatLogs(visibleLogs)}</pre>
        <XTerminal
          attachRef={attachTerm}
          readOnly
          fontSize={11}
          scrollback={1000}
          className="rounded-md"
        />
      </div>
    </div>
  );
}

function ResultDrawer({ result, onClose }: { result: ProbeResult; onClose(): void }) {
  const { tr } = useI18n();
  return (
    <div className="fixed inset-y-0 right-0 z-40 flex w-full max-w-xl flex-col border-l border-zinc-800 bg-zinc-950 shadow-2xl">
      <div className="flex items-center justify-between border-b border-zinc-800 px-4 py-3">
        <div>
          <h2 className="text-sm font-medium text-zinc-100">{result.edgeLabel}</h2>
          <p className="mt-0.5 text-xs text-zinc-500">{result.finishedAt ? new Date(result.finishedAt).toLocaleString() : tr('执行中', 'Running')}</p>
        </div>
        <Button onClick={onClose}><X size={13} />{tr('关闭', 'Close')}</Button>
      </div>
      <div className="flex-1 overflow-auto p-4">
        {result.error ? <div className="mb-3 rounded-lg border border-red-500/30 bg-red-500/5 px-3 py-2 text-xs text-red-300">{result.error}</div> : null}
        <pre className="min-h-96 overflow-auto rounded-lg border border-zinc-800 bg-zinc-900/40 px-3 py-2 font-mono text-xs leading-relaxed text-zinc-200">{formatResult(result.result)}</pre>
      </div>
    </div>
  );
}

function RunStatusChip({ status }: { status: RunStatus }) {
  const tone = status === 'success' ? 'success' : status === 'error' ? 'danger' : status === 'partial' ? 'warning' : 'info';
  return <Chip tone={tone}>{status}</Chip>;
}

function ProbeStatusChip({ status }: { status: ProbeStatus }) {
  if (status === 'running') return <span className="inline-flex items-center gap-1 text-sky-400"><Loader2 size={12} className="animate-spin" />running</span>;
  const tone = status === 'success' ? 'success' : status === 'error' ? 'danger' : 'warning';
  return <Chip tone={tone}>{status}</Chip>;
}

function CaptureStatusIcon({ status }: { status: CaptureQuickStatus }) {
  if (status === 'starting') return <Loader2 size={15} className="animate-spin text-sky-500" />;
  if (status === 'capturing') return <RadioTower size={15} className="text-sky-500" />;
  if (status === 'stopping') return <Loader2 size={15} className="animate-spin text-sky-500" />;
  if (status === 'ready') return <FileCode2 size={15} className="text-emerald-500" />;
  if (status === 'cancelled') return <Square size={15} className="text-amber-500" />;
  return <X size={15} className="text-red-500" />;
}

function CaptureMemberStateChip({ state }: { state: string }) {
  if (state === 'capturing' || state === 'dispatching' || state === 'queued' || state === 'uploading' || state === 'parsing') {
    return <span className="inline-flex items-center gap-1 text-sky-400"><Loader2 size={11} className="animate-spin" />{state}</span>;
  }
  const tone = state === 'ready' ? 'success' : state === 'failed' ? 'danger' : state === 'cancelled' ? 'warning' : 'default';
  return <Chip tone={tone}>{state}</Chip>;
}

function EdgeStatus({ edge }: { edge: Edge }) {
  const tone = edge.status === 'online' ? 'success' : edge.status === 'offline' ? 'danger' : 'warning';
  return <Chip tone={tone}>{edge.status}</Chip>;
}

function FieldGrid({ children }: { children: ReactNode }) {
  return <>{children}</>;
}

const inputClassName = 'h-9 w-full rounded-md border border-zinc-800 bg-zinc-950 px-2.5 py-0 text-[12px] leading-5 text-zinc-100 outline-none focus:border-zinc-600';

function TextField({ label, value, onChange, placeholder, className }: { label: ReactNode; value: string; onChange(value: string): void; placeholder?: string; className?: string }) {
  return (
    <label className={cn('w-72 shrink-0 space-y-1', className)}>
      <span className="text-[11px] leading-4 text-zinc-400">{label}</span>
      <input value={value} onChange={(event) => onChange(event.target.value)} placeholder={placeholder} className={cn(inputClassName, 'font-mono')} />
    </label>
  );
}

function NumberField({ label, value, onChange, min, max, className }: { label: ReactNode; value: string; onChange(value: string): void; min: number; max: number; className?: string }) {
  return (
    <label className={cn('w-36 shrink-0 space-y-1', className)}>
      <span className="text-[11px] leading-4 text-zinc-400">{label}</span>
      <input type="number" min={min} max={max} value={value} onChange={(event) => onChange(event.target.value)} className={cn(inputClassName, 'font-mono')} />
    </label>
  );
}

function Hint({ children }: { children: ReactNode }) {
  return <div className="min-w-72 flex-1 rounded-lg border border-zinc-800 bg-zinc-950/50 px-3 py-2 text-xs text-zinc-500">{children}</div>;
}

function operatorCommand(tool: Exclude<ToolKey, 'capture'>): OperatorCommand {
  switch (tool) {
    case 'ping': return 'ping';
    case 'dns': return 'dig';
    case 'tcp': return 'tcp';
    case 'http': return 'http';
  }
}

function toolParams(tool: Exclude<ToolKey, 'capture'>, forms: { ping: PingForm; dns: DNSForm; tcp: TCPForm; http: HTTPForm }, advanced: ExecAdvancedForm): Record<string, unknown> {
  switch (tool) {
    case 'ping':
      return withAdvancedParams({ host: forms.ping.host.trim(), count: numberOrDefault(forms.ping.count, 4), timeout_ms: numberOrDefault(forms.ping.timeout_ms, 3000) }, advanced);
    case 'dns':
      return withAdvancedParams({ host: forms.dns.host.trim(), type: 'A' }, advanced);
    case 'tcp': {
      const target = normalizeTCPForm(forms.tcp);
      return withAdvancedParams({ host: target.host, port: target.port, timeout_ms: numberOrDefault(forms.tcp.timeout_ms, 3000) }, advanced);
    }
    case 'http':
      return withAdvancedParams({ url: forms.http.url.trim(), method: forms.http.method, timeout_ms: numberOrDefault(forms.http.timeout_ms, 5000), skip_tls: forms.http.skip_tls }, advanced);
  }
}

function withAdvancedParams(args: Record<string, unknown>, advanced: ExecAdvancedForm) {
  const namespace = advanced.namespace.trim();
  return namespace ? { ...args, namespace } : args;
}

function toolTimeoutMs(tool: Exclude<ToolKey, 'capture'>, forms: { ping: PingForm; dns: DNSForm; tcp: TCPForm; http: HTTPForm }) {
  switch (tool) {
    case 'ping': return numberOrDefault(forms.ping.timeout_ms, 3000);
    case 'dns': return numberOrDefault(forms.dns.timeout_ms, 3000);
    case 'tcp': return numberOrDefault(forms.tcp.timeout_ms, 3000);
    case 'http': return numberOrDefault(forms.http.timeout_ms, 5000);
  }
}

function toolRunFromOperator(run: OperatorRun, tool: Exclude<ToolKey, 'capture'>, target: string, edges: Edge[], tr: (zh: string, en: string) => string): ToolRun {
  return {
    id: run.id,
    tool,
    target,
    title: run.title || `${toolLabel(tool, tr)} ${target}`,
    status: 'running',
    startedAt: run.started_at,
    results: edges.map((edge) => ({ edgeID: edge.id, edgeLabel: edgeLabel(edge), status: 'queued' })),
    logs: [],
  };
}

function edgeLabelByID(edges: Edge[], edgeID: number) {
  return edgeLabel(edges.find((edge) => edge.id === edgeID) ?? { id: edgeID, name: 'edge', status: 'unknown', roles: [], access_key_id: '', last_seen_at: null });
}

function toolCommand(tool: Exclude<ToolKey, 'capture'>, forms: { ping: PingForm; dns: DNSForm; tcp: TCPForm; http: HTTPForm }, advanced?: ExecAdvancedForm) {
  let command = '';
  switch (tool) {
    case 'ping':
      command = `ping -c ${numberOrDefault(forms.ping.count, 4)} -W ${Math.ceil(numberOrDefault(forms.ping.timeout_ms, 3000) / 1000)} ${forms.ping.host.trim()}`;
      break;
    case 'dns':
      command = `dig +time=${Math.ceil(numberOrDefault(forms.dns.timeout_ms, 3000) / 1000)} ${forms.dns.host.trim()} A AAAA`;
      break;
    case 'tcp': {
      const target = normalizeTCPForm(forms.tcp);
      command = `nc -vz -w ${Math.ceil(numberOrDefault(forms.tcp.timeout_ms, 3000) / 1000)} ${target.host} ${target.port}`;
      break;
    }
    case 'http': {
      const insecure = forms.http.skip_tls ? ' -k' : '';
      command = `curl -I -X ${forms.http.method} --max-time ${Math.ceil(numberOrDefault(forms.http.timeout_ms, 5000) / 1000)}${insecure} ${forms.http.url.trim()}`;
      break;
    }
  }
  const namespace = advanced?.namespace.trim();
  return namespace ? `ip netns exec ${namespace} ${command}` : command;
}

function captureCommand(form: CaptureForm, advanced?: ExecAdvancedForm) {
  const filter = form.filter.trim();
  const parts = [
    'tcpdump',
    '-U',
    '-n',
    '-q',
    '-i',
    form.interface_name.trim() || 'eth0',
    '-s',
    String(numberOrDefault(form.snaplen, 1514)),
    '-w',
    '<artifact>.pcap',
  ];
  if (filter) parts.push(JSON.stringify(filter));
  const command = parts.join(' ');
  const namespace = advanced?.namespace.trim();
  return namespace ? `ip netns exec ${namespace} ${command}` : command;
}

function toolTarget(tool: Exclude<ToolKey, 'capture'>, forms: { ping: PingForm; dns: DNSForm; tcp: TCPForm; http: HTTPForm }) {
  switch (tool) {
    case 'ping': return forms.ping.host.trim();
    case 'dns': return forms.dns.host.trim();
    case 'tcp': {
      const target = normalizeTCPForm(forms.tcp);
      return `${target.host}:${target.port}`;
    }
    case 'http': return forms.http.url.trim();
  }
}

function toolInputReady(tool: ToolKey, forms: { ping: PingForm; dns: DNSForm; tcp: TCPForm; http: HTTPForm; capture: CaptureForm }) {
  switch (tool) {
    case 'ping':
      return forms.ping.host.trim() !== '';
    case 'dns':
      return forms.dns.host.trim() !== '';
    case 'tcp':
      return forms.tcp.host.trim() !== '';
    case 'http':
      return forms.http.url.trim() !== '';
    case 'capture':
      return forms.capture.interface_name.trim() !== '';
  }
}

function normalizeTCPForm(form: TCPForm) {
  const hostInput = form.host.trim();
  const explicitPort = numberOrDefault(form.port, 443);
  const parsed = splitHostPortInput(hostInput);
  return {
    host: parsed.host || hostInput,
    port: parsed.port ?? explicitPort,
  };
}

function splitHostPortInput(input: string): { host: string; port?: number } {
  const value = input.trim();
  const bracketed = value.match(/^\[([^\]]+)\]:(\d+)$/);
  if (bracketed) return { host: bracketed[1], port: numberOrDefault(bracketed[2], 443) };
  const colon = value.lastIndexOf(':');
  if (colon <= 0 || colon !== value.indexOf(':')) return { host: value };
  const portText = value.slice(colon + 1);
  if (!/^\d+$/.test(portText)) return { host: value };
  return { host: value.slice(0, colon), port: numberOrDefault(portText, 443) };
}

function toolLabel(tool: ToolKey, tr: (zh: string, en: string) => string) {
  const item = TOOLS.find((entry) => entry.key === tool);
  return item ? tr(item.zh, item.en) : tool;
}

function edgeLabel(edge: Edge) {
  const host = hostInfoValue(edge.host_info, 'hostname');
  return `#${edge.id} ${edge.device_name || host || edge.name || 'edge'}`;
}

function edgeSearchText(edge: Edge) {
  return `${edge.id} ${edge.name} ${edge.device_name ?? ''} ${hostInfoValue(edge.host_info, 'hostname')} ${edge.status} ${edge.device_id ?? ''}`.toLowerCase();
}

function edgeMetaLine(edge: Edge) {
  const parts = [edge.name, edge.device_id ? `device ${edge.device_id}` : ''].filter(Boolean);
  return parts.length > 0 ? parts.join(' · ') : 'Edge';
}

function hostInfoValue(value: Edge['host_info'], key: string) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return '';
  const got = value[key];
  return typeof got === 'string' ? got : '';
}

function sessionLink(session: PacketCaptureSession) {
  return session.id ? `/artifacts/packet-sessions/${encodeURIComponent(session.id)}` : '/pages?tab=packets';
}

function captureQuickLink(captureItem: PacketCapture) {
  if (captureItem.artifact_id) return `/artifacts/packets/${encodeURIComponent(packetCaptureArtifactID(captureItem))}`;
  return '/pages?tab=packets';
}

function captureMemberFromCapture(captureItem: PacketCapture, fallbackEdgeLabel?: string): CaptureQuickMember {
  return {
    id: captureItem.id,
    edgeLabel: fallbackEdgeLabel || `#${captureItem.edge_id || captureItem.device_id} edge`,
    state: captureItem.state,
    capturedPackets: captureItem.captured_packets,
    capturedBytes: captureItem.captured_bytes,
    livePreview: captureItem.live_preview,
    startedAt: captureItem.started_at,
    finishedAt: captureItem.finished_at,
    error: captureItem.error_detail || captureItem.error_code,
  };
}

function appendedPreviewLines(previous: string[], next: string[]) {
  if (next.length === 0) return [];
  const limit = Math.min(previous.length, next.length);
  for (let overlap = limit; overlap > 0; overlap -= 1) {
    const previousTail = previous.slice(previous.length - overlap);
    const nextHead = next.slice(0, overlap);
    if (previousTail.every((line, index) => line === nextHead[index])) return next.slice(overlap);
  }
  return next;
}

function captureStateIsTerminal(state: string) {
  return state === 'ready' || state === 'cancelled' || state === 'failed' || state === 'raw_expired' || state === 'expired' || state === 'deleted';
}

function numberOrDefault(value: string, fallback: number) {
  const n = Number(value);
  return Number.isFinite(n) && n > 0 ? n : fallback;
}

function nextStatus(failed: number, total: number): RunStatus {
  if (failed === 0) return 'success';
  return failed === total ? 'error' : 'partial';
}

function captureRunStatus(status: CaptureQuickStatus): RunStatus {
  if (status === 'capturing' || status === 'starting' || status === 'stopping') return 'running';
  if (status === 'ready') return 'success';
  if (status === 'cancelled') return 'partial';
  return 'error';
}

function runLog(event: Omit<RunLogEvent, 'id' | 'ts'>): RunLogEvent {
  return {
    ...event,
    id: crypto.randomUUID(),
    ts: new Date().toISOString(),
    message: String(event.message ?? ''),
  };
}

function appendSkillResultLogs(
  runID: string,
  edgeLabelText: string,
  result: unknown,
  error: string | undefined,
  append: (runID: string, event: Omit<RunLogEvent, 'id' | 'ts'>) => void,
) {
  const output = extractOutput(result);
  if (output.stdout) append(runID, { stream: 'stdout', edgeLabel: edgeLabelText, message: output.stdout });
  if (output.stderr) append(runID, { stream: 'stderr', edgeLabel: edgeLabelText, message: output.stderr });
  if (!output.stdout && !output.stderr && result !== undefined) append(runID, { stream: 'stdout', edgeLabel: edgeLabelText, message: formatResult(result) });
  if (error) append(runID, { stream: 'stderr', edgeLabel: edgeLabelText, message: error });
}

function extractOutput(result: unknown): { stdout: string; stderr: string } {
  if (typeof result === 'string') return { stdout: result, stderr: '' };
  if (!result || typeof result !== 'object') return { stdout: '', stderr: '' };
  const obj = result as { stdout?: unknown; stderr?: unknown; output?: unknown };
  return {
    stdout: typeof obj.stdout === 'string' ? obj.stdout : typeof obj.output === 'string' ? obj.output : '',
    stderr: typeof obj.stderr === 'string' ? obj.stderr : '',
  };
}

function formatLogsAnsi(logs: RunLogEvent[]): string {
  return logs.flatMap((event) => {
    const time = ansiDim(timeOnly(event.ts));
    const prefix = event.edgeLabel ? `${ansiDim(`[${event.edgeLabel}] `)}` : '';
    return splitTerminalLines(redactTerminalText(event.message)).map((line) => (
      line === '' ? '' : `${time}  ${prefix}${ansiForStream(event.stream, line)}`
    ));
  }).join('\r\n') + '\r\n';
}

function splitTerminalLines(value: string) {
  const normalized = value.replace(/\r\n/g, '\n').replace(/\r/g, '\n');
  const lines = normalized.split('\n');
  if (lines.length > 1 && lines[lines.length - 1] === '') lines.pop();
  return lines.length > 0 ? lines : [''];
}

function ansiForStream(stream: RunLogStream, message: string) {
  switch (stream) {
    case 'stderr': return `\x1b[31m${message}\x1b[0m`;
    case 'stdout': return `\x1b[37m${message}\x1b[0m`;
    case 'status': return `\x1b[36m${message}\x1b[0m`;
    case 'system': return ansiDim(message);
  }
}

function ansiDim(message: string) {
  return `\x1b[2m${message}\x1b[0m`;
}

function formatLogs(logs: RunLogEvent[]): string {
  return logs.flatMap((event) => {
    const prefix = `${timeOnly(event.ts)} ${event.edgeLabel ? `[${event.edgeLabel}] ` : ''}`;
    return splitTerminalLines(redactTerminalText(event.message)).map((line) => (line === '' ? '' : `${prefix}${line}`));
  }).join('\n');
}

function timeOnly(ts: string) {
  return new Date(ts).toLocaleTimeString([], { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

function elapsedLabel(startedAt?: string, finishedAt?: string, nowMs = Date.now()) {
  if (!startedAt) return '0s';
  const start = new Date(startedAt).getTime();
  const end = finishedAt ? new Date(finishedAt).getTime() : nowMs;
  if (!Number.isFinite(start) || !Number.isFinite(end)) return '0s';
  const totalSeconds = Math.max(0, Math.round((end - start) / 1000));
  if (totalSeconds < 60) return `${totalSeconds}s`;
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${minutes}m ${seconds}s`;
}

function resultDuration(result: ProbeResult, nowMs: number) {
  if (result.durationMs !== undefined) return `${result.durationMs}ms`;
  if (result.status === 'running' && result.startedAt) return elapsedLabel(result.startedAt, undefined, nowMs);
  return '-';
}

function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes)) return '-';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function redactTerminalText(text: string) {
  return text
    .replace(/(authorization:\s*bearer\s+)[^\s]+/gi, '$1[REDACTED]')
    .replace(/((?:password|passwd|token|secret|api[_-]?key)\s*[=:]\s*)[^\s'"&]+/gi, '$1[REDACTED]');
}

function pingMetrics(result: unknown): { loss?: string; avg?: string; p95?: string } {
  const text = typeof result === 'string' ? result : typeof result === 'object' && result !== null ? String((result as { stdout?: unknown }).stdout ?? '') : '';
  const loss = text.match(/(\d+(?:\.\d+)?)%\s*packet loss/i)?.[1];
  const rtt = text.match(/(?:rtt|round-trip).*?=\s*([\d.]+)\/([\d.]+)\/([\d.]+)\/([\d.]+)/i);
  return { loss: loss ? `${loss}%` : undefined, avg: rtt ? `${rtt[2]}ms` : undefined, p95: rtt ? `${rtt[3]}ms` : undefined };
}

function formatRun(run: ToolRun): string {
  return JSON.stringify({ tool: run.tool, target: run.target, status: run.status, results: run.results }, null, 2);
}

function formatResult(result: unknown): string {
  if (result === undefined || result === null) return '';
  if (typeof result === 'string') return result;
  try {
    return JSON.stringify(result, null, 2);
  } catch {
    return String(result);
  }
}

async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    // Clipboard is a convenience action; failing it should not disturb the run.
  }
}
