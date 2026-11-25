import { useEffect, useState, type ReactNode } from 'react';

import { api, megabytes, when, type Stats, type SystemInfo, type Task } from '../api';
import { useSession } from '../session';
import { Badge, Card, CardHead, Empty, errorMessage, IconActivity, IconGlobe, IconPause, IconUsers, Meter, statusTone } from '../ui';

interface DashboardResponse {
    stats: Stats;
    tasks: Task[];
    system: SystemInfo | null;
    agent: string;
    agentError?: string;
}

export function Dashboard() {
    const { user } = useSession();
    const [data, setData] = useState<DashboardResponse | null>(null);
    const [error, setError] = useState('');

    useEffect(() => {
        let live = true;

        api<DashboardResponse>('/dashboard')
            .then((response) => {
                if (live) {
                    setData(response);
                }
            })
            .catch((thrown: unknown) => {
                if (live) {
                    setError(errorMessage(thrown));
                }
            });

        return () => {
            live = false;
        };
    }, []);

    const stats = data?.stats;
    const system = data?.system;

    const greeting =
        user.role === 'admin' ? 'Everything on this server' : 'Your accounts and their activity';

    return (
        <div className="page">
            <header className="page-head">
                <div>
                    <h1 className="page-title">Overview</h1>
                    <p className="page-sub">{greeting}</p>
                </div>
                <span className="agent-line">
                    <span className={`dot ${data?.agent === 'socket' ? '' : 'sim'}`} />
                    {data?.agent === 'socket' ? 'agent live' : 'simulated'}
                </span>
            </header>

            {error ? <div className="banner bad">{error}</div> : null}

            {data?.agentError ? (
                <div className="banner warn">
                    The server agent is not answering, so the numbers below are from the panel's own
                    database: {data.agentError}
                </div>
            ) : null}

            <div className="grid-4">
                <Stat label="Hosting accounts" value={stats?.accounts} icon={<IconUsers />} foot={`${stats?.active ?? 0} active`} />
                <Stat
                    label="Suspended"
                    icon={<IconPause />}
                    value={stats?.suspended}
                    foot={stats?.pending ? `${stats.pending} waiting to start` : 'none waiting'}
                />
                <Stat label="Domains" value={stats?.domains} icon={<IconGlobe />} foot="websites served" />
                <Stat
                    label="Queued work"
                    icon={<IconActivity />}
                    value={stats?.tasksQueued}
                    foot={stats?.tasksFailed ? `${stats.tasksFailed} failed recently` : 'nothing failing'}
                    tone={stats?.tasksQueued ? 'bad' : undefined}
                />
            </div>

            {/* The left column carries two cards, so the row ends level with the
               activity list instead of leaving a hole under the short one. */}
            <div className="grid-2 top">
                <div className="stack">
                <Card>
                    <CardHead title="Server">
                        {system?.hostname ? <span className="muted mono">{system.hostname}</span> : null}
                    </CardHead>
                    <div className="card-body">
                        {system ? (
                            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
                                <Gauge
                                    label="Memory"
                                    used={system.memory.used_mb}
                                    total={system.memory.total_mb}
                                    format={megabytes}
                                />
                                <Gauge
                                    label={`Disk (${system.disk.mount})`}
                                    used={system.disk.used_gb * 1024}
                                    total={system.disk.total_gb * 1024}
                                    format={megabytes}
                                />
                                <dl className="kv">
                                    <dt>Load</dt>
                                    <dd>
                                        {system.load.slice(0, 3).map((value) => value.toFixed(2)).join('  ') || '—'}
                                        {` on ${system.cpu.cores} cores`}
                                    </dd>
                                    <dt>System</dt>
                                    <dd>
                                        {system.os.name} {system.os.version}
                                    </dd>
                                    <dt>Kernel</dt>
                                    <dd className="mono">{system.os.kernel}</dd>
                                    <dt>Uptime</dt>
                                    <dd>{uptime(system.uptime_seconds)}</dd>
                                </dl>
                            </div>
                        ) : (
                            <div className="skeleton" style={{ height: 132 }}>
                                .
                            </div>
                        )}
                    </div>
                </Card>

                {user.role === 'admin' ? (
                    <Card>
                        <CardHead title="Resellers" />
                        <div className="card-body row" style={{ justifyContent: 'space-between' }}>
                            <span className="muted">
                                {stats?.resellers ?? 0} reseller{stats?.resellers === 1 ? '' : 's'} manage their
                                own accounts on this server.
                            </span>
                            <a className="btn sm" href="/accounts">
                                View accounts
                            </a>
                        </div>
                    </Card>
                ) : null}

                {user.role === 'admin' ? (
                    <Card>
                        <CardHead title="Quick actions" />
                        <div className="card-body row">
                            <a className="btn" href="/accounts">
                                New account
                            </a>
                            <a className="btn" href="/domains">
                                New website
                            </a>
                            <a className="btn" href="/mail">
                                New mailbox
                            </a>
                            <a className="btn" href="/databases">
                                New database
                            </a>
                        </div>
                    </Card>
                ) : null}
                </div>

                <Card>
                    <CardHead title="Recent activity">
                        <a className="btn sm ghost" href="/activity">
                            All activity
                        </a>
                    </CardHead>
                    {data && data.tasks.length === 0 ? (
                        <Empty title="Nothing has happened yet">
                            Creating an account or a website shows up here.
                        </Empty>
                    ) : (
                        <div className="table-wrap">
                            <table>
                                <tbody>
                                    {(data?.tasks ?? []).map((task) => (
                                        <tr key={task.id}>
                                            <td>
                                                <div className="cell-main">
                                                    <span className="strong">{task.action}</span>
                                                    <span className="cell-sub">
                                                        {task.username ?? 'server'} · {when(task.createdAt)}
                                                    </span>
                                                </div>
                                            </td>
                                            <td style={{ textAlign: 'right' }}>
                                                <Badge tone={statusTone(task.status)}>{task.status}</Badge>
                                            </td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        </div>
                    )}
                </Card>
            </div>
        </div>
    );
}

function Stat({
    label,
    value,
    foot,
    tone,
    icon,
}: {
    label: string;
    value: number | undefined;
    foot: string;
    tone?: 'bad';
    icon?: ReactNode;
}) {
    return (
        <Card>
            <div className="stat">
                {icon ? <span className="stat-icon">{icon}</span> : null}
                <span className="stat-label">{label}</span>
                <span className="stat-value" style={tone === 'bad' ? { color: 'var(--red)' } : undefined}>
                    {value === undefined ? <span className="skeleton">000</span> : value}
                </span>
                <span className="stat-foot">{foot}</span>
            </div>
        </Card>
    );
}

function Gauge({
    label,
    used,
    total,
    format,
}: {
    label: string;
    used: number;
    total: number;
    format: (value: number) => string;
}) {
    const percent = total > 0 ? Math.round((used / total) * 100) : 0;

    return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 7 }}>
            <div className="row" style={{ justifyContent: 'space-between' }}>
                <span className="muted">{label}</span>
                <span className="num">
                    {format(used)} <span className="muted">of {format(total)}</span>
                </span>
            </div>
            <Meter value={used} ceiling={total} tone={percent >= 90 ? 'bad' : percent >= 75 ? 'warn' : 'ok'} />
        </div>
    );
}

function uptime(seconds: number | undefined): string {
    if (!seconds || seconds <= 0) {
        return '—';
    }

    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);

    if (days > 0) {
        return `${days} d ${hours} h`;
    }

    return `${hours} h ${Math.floor((seconds % 3600) / 60)} m`;
}
