import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';

import { api, when, type Domain } from '../api';
import { linkProps } from '../router';
import { useSession } from '../session';
import {
    Badge,
    Button,
    Card,
    Drawer,
    Empty,
    errorMessage,
    Field,
    IconPlus,
    SkeletonRows,
} from '../ui';

interface NodeApp {
    id: number;
    domainId: number;
    domain: string;
    username: string;
    entry: string;
    env: Record<string, string>;
    port: number;
    enabled: boolean;
    status: string;
    createdAt: string;
}

function appTone(status: string): 'ok' | 'warn' | 'bad' | 'plain' {
    switch (status) {
        case 'active':
            return 'ok';
        case 'pending':
            return 'warn';
        case 'failed':
            return 'bad';
        default:
            return 'plain';
    }
}

/** Node.js apps: one long-running process behind a website, reverse-proxied
 *  by nginx. The row records intent; the agent owns the actual process. */
export function NodeApps() {
    const { user } = useSession();
    const [apps, setApps] = useState<NodeApp[] | null>(null);
    const [error, setError] = useState('');
    const [busy, setBusy] = useState('');
    const [creating, setCreating] = useState(false);

    const load = useCallback(async () => {
        try {
            const answer = await api<{ apps: NodeApp[] }>('/node-apps');
            setApps(answer.apps);
            setError('');
        } catch (thrown) {
            setError(errorMessage(thrown));
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    async function restart(app: NodeApp): Promise<void> {
        setBusy(`restart-${app.id}`);

        try {
            await api(`/node-apps/${app.id}/restart`, { method: 'POST' });
            await load();
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy('');
        }
    }

    async function remove(app: NodeApp): Promise<void> {
        if (!window.confirm(`Stop the app on ${app.domain} and remove it?`)) {
            return;
        }

        setBusy(`delete-${app.id}`);

        try {
            await api(`/node-apps/${app.id}`, { method: 'DELETE' });
            await load();
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy('');
        }
    }

    return (
        <div className="page">
            <header className="page-head">
                <div>
                    <h1 className="page-title">Node apps</h1>
                    <p className="page-sub">
                        {apps === null
                            ? 'Loading'
                            : `${apps.length} long-running Node.js ${apps.length === 1 ? 'app' : 'apps'}, proxied behind their websites`}
                    </p>
                </div>
                <Button variant="primary" onClick={() => setCreating(true)}>
                    <IconPlus /> New app
                </Button>
            </header>

            {error ? <div className="banner bad">{error}</div> : null}

            <Card>
                <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Domain</th>
                                <th>Entry</th>
                                <th>Port</th>
                                <th>Status</th>
                                <th>Created</th>
                                <th style={{ textAlign: 'right' }}>Actions</th>
                            </tr>
                        </thead>

                        {apps === null ? (
                            <SkeletonRows rows={4} columns={6} />
                        ) : apps.length === 0 ? null : (
                            <tbody>
                                {apps.map((app) => (
                                    <tr key={app.id}>
                                        <td>
                                            <div className="cell-main">
                                                <a className="strong mono" {...linkProps(`/domains/${app.domainId}`)}>
                                                    {app.domain}
                                                </a>
                                                <span className="cell-sub">{app.username}</span>
                                            </div>
                                        </td>
                                        <td className="mono">{app.entry}</td>
                                        <td className="num">{app.port}</td>
                                        <td>
                                            <Badge tone={appTone(app.status)}>{app.status}</Badge>
                                        </td>
                                        <td className="muted">{when(app.createdAt)}</td>
                                        <td>
                                            <div className="row" style={{ justifyContent: 'flex-end', gap: 6 }}>
                                                <Button
                                                    size="sm"
                                                    variant="ghost"
                                                    busy={busy === `restart-${app.id}`}
                                                    onClick={() => void restart(app)}
                                                >
                                                    Restart
                                                </Button>
                                                <Button
                                                    size="sm"
                                                    variant="ghost danger"
                                                    busy={busy === `delete-${app.id}`}
                                                    onClick={() => void remove(app)}
                                                >
                                                    Delete
                                                </Button>
                                            </div>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        )}
                    </table>
                </div>

                {apps !== null && apps.length === 0 ? (
                    <Empty title="No Node.js apps yet">
                        {user.role === 'user'
                            ? 'Open a website and give it an entry script to run its own process.'
                            : 'Give a website an entry script and it runs its own Node process behind nginx.'}
                    </Empty>
                ) : null}
            </Card>

            {creating ? (
                <NewApp
                    isAdmin={user.role === 'admin'}
                    onClose={() => setCreating(false)}
                    onCreated={() => {
                        setCreating(false);
                        void load();
                    }}
                />
            ) : null}
        </div>
    );
}

function NewApp({
    isAdmin,
    onClose,
    onCreated,
}: {
    isAdmin: boolean;
    onClose: () => void;
    onCreated: () => void;
}) {
    const [domains, setDomains] = useState<(Domain & { username?: string })[]>([]);
    const [domainID, setDomainID] = useState('');
    const [entry, setEntry] = useState('app.js');
    const [envPairs, setEnvPairs] = useState<{ key: string; value: string }[]>([{ key: '', value: '' }]);
    const [error, setError] = useState('');
    const [busy, setBusy] = useState(false);

    useEffect(() => {
        const path = isAdmin ? '/domains?limit=200' : '/domains';

        void api<{ domains: (Domain & { username?: string })[] }>(path).then((answer) => {
            setDomains(answer.domains);

            if (answer.domains[0]) {
                setDomainID(String(answer.domains[0].id));
            }
        });
    }, [isAdmin]);

    async function save(event: FormEvent): Promise<void> {
        event.preventDefault();
        setBusy(true);
        setError('');

        const env: Record<string, string> = {};

        for (const pair of envPairs) {
            if (pair.key.trim()) {
                env[pair.key.trim()] = pair.value;
            }
        }

        try {
            await api('/node-apps', {
                method: 'POST',
                body: { domainId: Number(domainID), entry, env },
            });
            onCreated();
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy(false);
        }
    }

    return (
        <Drawer
            title="New Node app"
            subtitle="Runs an entry script as the account's own process; nginx proxies the website to it."
            onClose={onClose}
            footer={
                <>
                    <Button variant="ghost" onClick={onClose}>
                        Cancel
                    </Button>
                    <Button type="submit" variant="primary" busy={busy} form="new-node-app">
                        Start app
                    </Button>
                </>
            }
        >
            <form id="new-node-app" className="form" onSubmit={save}>
                {error ? <div className="banner bad">{error}</div> : null}

                <label className="field">
                    <span className="field-label">Website</span>
                    <select
                        className="input"
                        value={domainID}
                        onChange={(event) => setDomainID(event.target.value)}
                        required
                    >
                        {domains.map((domain) => (
                            <option key={domain.id} value={domain.id}>
                                {domain.name}
                                {domain.username ? ` (${domain.username})` : ''}
                            </option>
                        ))}
                    </select>
                    <span className="field-hint">One app per website; a website already running one is refused.</span>
                </label>

                <Field
                    label="Entry script"
                    value={entry}
                    onChange={(event) => setEntry(event.target.value)}
                    hint="Relative to the document root: app.js, dist/server.js, build/index.js."
                    required
                />

                <div className="field">
                    <span className="field-label">Environment variables</span>
                    {envPairs.map((pair, index) => (
                        <div className="row" key={index} style={{ gap: 8, flexWrap: 'nowrap' }}>
                            <input
                                className="input"
                                placeholder="NAME"
                                value={pair.key}
                                onChange={(event) => {
                                    const next = [...envPairs];

                                    next[index] = { ...pair, key: event.target.value };
                                    setEnvPairs(next);
                                }}
                            />
                            <input
                                className="input"
                                placeholder="value"
                                value={pair.value}
                                onChange={(event) => {
                                    const next = [...envPairs];

                                    next[index] = { ...pair, value: event.target.value };
                                    setEnvPairs(next);
                                }}
                            />
                            <Button
                                type="button"
                                size="sm"
                                variant="ghost"
                                onClick={() => setEnvPairs(envPairs.filter((_, at) => at !== index))}
                                aria-label="Remove variable"
                            >
                                ×
                            </Button>
                        </div>
                    ))}
                    <Button
                        type="button"
                        size="sm"
                        variant="ghost"
                        onClick={() => setEnvPairs([...envPairs, { key: '', value: '' }])}
                    >
                        <IconPlus /> Add variable
                    </Button>
                    <span className="field-hint">
                        Written to a 0600 environment file owned by the account; at most 40, one line each.
                    </span>
                </div>
            </form>
        </Drawer>
    );
}
