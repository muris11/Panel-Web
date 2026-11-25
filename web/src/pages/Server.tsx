import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';

import { api, type ServerPage, type ServiceRow } from '../api';
import { Badge, Button, Card, CardHead, Empty, errorMessage, Field, SkeletonRows, statusTone } from '../ui';

export function Server({ onToast }: { onToast: (message: string, tone?: 'ok' | 'bad') => void }) {
    const [page, setPage] = useState<ServerPage | null>(null);
    const [usage, setUsage] = useState<Record<string, unknown> | null>(null);
    const [error, setError] = useState('');
    const [busy, setBusy] = useState('');

    const load = useCallback(async () => {
        try {
            const [server, used] = await Promise.all([
                api<ServerPage>('/server'),
                api<Record<string, unknown>>('/usage').catch(() => null),
            ]);

            setPage(server);
            setUsage(used);
            setError('');
        } catch (thrown) {
            setError(errorMessage(thrown));
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    async function act(service: ServiceRow, action: string): Promise<void> {
        setBusy(`${service.unit}:${action}`);

        try {
            await api('/server/services', { method: 'POST', body: { unit: service.unit, action } });
            onToast(`${service.label || service.unit}: ${action} sent`);
            await load();
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy('');
        }
    }

    async function save(event: FormEvent<HTMLFormElement>): Promise<void> {
        event.preventDefault();

        const data = new FormData(event.currentTarget);
        const hostname = String(data.get('hostname') ?? '');
        const timezone = String(data.get('timezone') ?? '');

        setBusy('settings');

        try {
            await api('/server', { method: 'PATCH', body: { hostname, timezone } });
            onToast('The server settings are being applied');
            event.currentTarget.reset();
            await load();
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy('');
        }
    }

    const services = page?.services?.services ?? null;
    const firewall = page?.firewall;
    const settings = page?.settings ?? {};

    const bandwidth = usage?.bandwidth as
        | { month?: string; total_bytes?: number; web_bytes?: number; mail_bytes?: number; ftp_bytes?: number }
        | null
        | undefined;

    const collected = (usage?.collectors ?? []) as {
        job?: string;
        finished_at?: string;
        ok?: boolean;
        detail?: string;
    }[];

    const size = (bytes: number) => {
        if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(2)} GB`;
        if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
        if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;

        return `${bytes} B`;
    };

    return (
        <div className="page">
            <header className="page-head">
                <div>
                    <h1 className="page-title">Server</h1>
                    <p className="page-sub">
                        {String(settings.hostname ?? 'This machine')}
                        {settings.operating_system ? ` · ${String(settings.operating_system)}` : ''}
                    </p>
                </div>
                <Button onClick={() => void load()}>Refresh</Button>
            </header>

            {error ? <div className="banner bad">{error}</div> : null}

            {page && Object.keys(page.problems ?? {}).length > 0 ? (
                <div className="banner">
                    The agent could not answer everything:{' '}
                    {Object.entries(page.problems)
                        .map(([key, value]) => `${key} (${value})`)
                        .join(', ')}
                </div>
            ) : null}

            <div className="grid-2">
                <Card>
                    <CardHead title="Services">
                        <Badge tone="plain">{services?.length ?? 0}</Badge>
                    </CardHead>

                    <div className="table-wrap">
                        <table>
                            <thead>
                                <tr>
                                    <th>Service</th>
                                    <th>State</th>
                                    <th style={{ textAlign: 'right' }}>Actions</th>
                                </tr>
                            </thead>
                            <tbody>
                                {services === null ? (
                                    <SkeletonRows rows={4} columns={3} />
                                ) : (
                                    services.map((service) => (
                                        <tr key={service.unit}>
                                            <td>
                                                <div className="cell-main">
                                                    <span className="strong">{service.label || service.unit}</span>
                                                    <span className="cell-sub mono">{service.unit}</span>
                                                </div>
                                            </td>
                                            <td>
                                                <Badge tone={statusTone(service.state)}>{service.state}</Badge>
                                            </td>
                                            <td>
                                                <div className="row" style={{ justifyContent: 'flex-end', gap: 6 }}>
                                                    <Button
                                                        size="sm"
                                                        variant="ghost"
                                                        busy={busy === `${service.unit}:restart`}
                                                        onClick={() => void act(service, 'restart')}
                                                    >
                                                        Restart
                                                    </Button>
                                                    <Button
                                                        size="sm"
                                                        variant="ghost"
                                                        busy={busy === `${service.unit}:reload`}
                                                        onClick={() => void act(service, 'reload')}
                                                    >
                                                        Reload
                                                    </Button>
                                                </div>
                                            </td>
                                        </tr>
                                    ))
                                )}
                            </tbody>
                        </table>
                    </div>

                    {services !== null && services.length === 0 ? <Empty title="No managed services reported" /> : null}
                </Card>

                <Card>
                    <CardHead title="Firewall">
                        <Badge tone={firewall?.enabled ? 'ok' : 'warn'}>{firewall?.enabled ? 'on' : 'off'}</Badge>
                    </CardHead>

                    <div className="table-wrap">
                        <table>
                            <thead>
                                <tr>
                                    <th>Port</th>
                                    <th>Protocol</th>
                                    <th>Action</th>
                                </tr>
                            </thead>
                            <tbody>
                                {(firewall?.rules ?? []).map((rule) => (
                                    <tr key={`${rule.port}-${rule.protocol}`}>
                                        <td className="mono">{rule.port}</td>
                                        <td>{rule.protocol}</td>
                                        <td>
                                            <Badge tone={rule.action === 'allow' ? 'ok' : 'bad'}>{rule.action}</Badge>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>

                    {firewall && (firewall.rules ?? []).length === 0 ? <Empty title="The firewall has no rules reported" /> : null}
                </Card>
            </div>

            <div className="grid-2">
                <Card>
                    <CardHead title="Server settings" />
                    <form className="card-body form" onSubmit={(event) => void save(event)}>
                        <Field
                            label="Hostname"
                            name="hostname"
                            defaultValue={String(settings.hostname ?? '')}
                            hint="Must resolve to this server before a certificate can be issued"
                        />
                        <Field label="Timezone" name="timezone" defaultValue={String(settings.timezone ?? '')} />
                        <div className="row" style={{ justifyContent: 'flex-end' }}>
                            <Button type="submit" variant="primary" busy={busy === 'settings'}>
                                Save settings
                            </Button>
                        </div>
                    </form>
                </Card>

                <Card>
                    <CardHead title="Bandwidth" />
                    <div className="card-body">
                        <p className="muted">This month ({bandwidth?.month ?? '—'})</p>
                        <dl className="kv">
                            <dt>Total</dt>
                            <dd>{size(bandwidth?.total_bytes ?? 0)}</dd>
                            <dt>Web</dt>
                            <dd>{size(bandwidth?.web_bytes ?? 0)}</dd>
                            <dt>Mail</dt>
                            <dd>{size(bandwidth?.mail_bytes ?? 0)}</dd>
                            <dt>FTP</dt>
                            <dd>{size(bandwidth?.ftp_bytes ?? 0)}</dd>
                        </dl>
                        {collected.length > 0 ? (
                            <p className="muted" style={{ marginBottom: 0, fontSize: 12 }}>
                                {collected.some((run) => run.ok === false)
                                    ? 'The last collection reported a problem; the numbers may be behind.'
                                    : `Counted from the server logs. Last update ${
                                          collected[0]?.finished_at
                                              ? new Date(collected[0].finished_at).toLocaleString()
                                              : 'just now'
                                      }.`}
                            </p>
                        ) : (
                            <p className="muted" style={{ marginBottom: 0, fontSize: 12 }}>
                                No collection has run yet. It runs every fifteen minutes, or on demand
                                with <code>muriscore-panel collect</code>.
                            </p>
                        )}
                    </div>
                </Card>
            </div>
        </div>
    );
}
