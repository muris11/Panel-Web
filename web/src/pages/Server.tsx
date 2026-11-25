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
        | { month?: string; usage?: { total_gb?: number; in_gb?: number; out_gb?: number } }
        | null
        | undefined;

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
                                                        busy={busy === `${service.unit}:restart`}
                                                        onClick={() => void act(service, 'restart')}
                                                    >
                                                        Restart
                                                    </Button>
                                                    <Button
                                                        size="sm"
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
                        {bandwidth?.usage ? (
                            <>
                                <p className="muted">This month ({bandwidth.month})</p>
                                <dl className="kv">
                                    <dt>Total</dt>
                                    <dd>{(bandwidth.usage.total_gb ?? 0).toFixed(1)} GB</dd>
                                    <dt>In</dt>
                                    <dd>{(bandwidth.usage.in_gb ?? 0).toFixed(1)} GB</dd>
                                    <dt>Out</dt>
                                    <dd>{(bandwidth.usage.out_gb ?? 0).toFixed(1)} GB</dd>
                                </dl>
                                <p className="muted" style={{ marginBottom: 0, fontSize: 12 }}>
                                    The agent reports totals without a plan ceiling, so no bar is drawn.
                                </p>
                            </>
                        ) : (
                            <p className="muted">The agent does not report bandwidth on this server yet.</p>
                        )}
                    </div>
                </Card>
            </div>
        </div>
    );
}
