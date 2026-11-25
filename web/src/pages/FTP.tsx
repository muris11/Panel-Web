import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';

import { api, type Account, type DomainDetail, type FTPLogin } from '../api';
import { Badge, Button, Card, Drawer, Empty, errorMessage, Field, SkeletonRows, statusTone } from '../ui';

export function FTP({ onToast }: { onToast: (message: string, tone?: 'ok' | 'bad') => void }) {
    const [logins, setLogins] = useState<FTPLogin[] | null>(null);
    const [error, setError] = useState('');
    const [creating, setCreating] = useState(false);
    const [busy, setBusy] = useState('');

    const load = useCallback(async () => {
        try {
            const response = await api<{ accounts: FTPLogin[] }>('/ftp');
            setLogins(response.accounts);
            setError('');
        } catch (thrown) {
            setError(errorMessage(thrown));
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    async function remove(login: FTPLogin): Promise<void> {
        if (!window.confirm(`Remove the FTP login ${login.login}?`)) {
            return;
        }

        setBusy(login.login);

        try {
            await api(`/ftp/${login.id}`, { method: 'DELETE' });
            onToast(`${login.login} removed`);
            await load();
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy('');
        }
    }

    async function resetPassword(login: FTPLogin): Promise<void> {
        if (!window.confirm(`Give ${login.login} a new password?`)) {
            return;
        }

        setBusy(login.login);

        try {
            const answer = await api<{ password: string }>(`/ftp/${login.id}`, { method: 'PATCH', body: {} });
            onToast(`New password for ${login.login}: ${answer.password}`);
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy('');
        }
    }

    return (
        <div className="page">
            <header className="page-head">
                <div>
                    <h1 className="page-title">FTP</h1>
                    <p className="page-sub">
                        {logins === null ? 'Loading' : `${logins.length} login${logins.length === 1 ? '' : 's'}`}
                    </p>
                </div>
                <Button variant="primary" onClick={() => setCreating(true)}>
                    New FTP login
                </Button>
            </header>

            {error ? <div className="banner bad">{error}</div> : null}

            <Card>
                <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Login</th>
                                <th>Account</th>
                                <th>Folder</th>
                                <th className="num">Quota</th>
                                <th>Status</th>
                                <th style={{ textAlign: 'right' }}>Actions</th>
                            </tr>
                        </thead>

                        {logins === null ? (
                            <SkeletonRows rows={4} columns={6} />
                        ) : logins.length === 0 ? null : (
                            <tbody>
                                {logins.map((login) => (
                                    <tr key={login.id}>
                                        <td className="mono strong">{login.login}</td>
                                        <td className="mono">{login.username}</td>
                                        <td className="mono muted">{login.directory || '/'}</td>
                                        <td className="num">{login.quotaMb ? `${login.quotaMb} MB` : '—'}</td>
                                        <td>
                                            <Badge tone={statusTone(login.status)}>{login.status}</Badge>
                                        </td>
                                        <td>
                                            <div className="row" style={{ justifyContent: 'flex-end', gap: 6 }}>
                                                <Button
                                                    size="sm"
                                                    busy={busy === login.login}
                                                    onClick={() => void resetPassword(login)}
                                                >
                                                    New password
                                                </Button>
                                                <Button
                                                    size="sm"
                                                    busy={busy === login.login}
                                                    onClick={() => void remove(login)}
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

                {logins !== null && logins.length === 0 ? (
                    <Empty title="No FTP logins yet">
                        An FTP login is tied to one account and one folder inside it, so it can never reach the rest
                        of the server.
                    </Empty>
                ) : null}
            </Card>

            {creating ? (
                <AddLogin
                    onClose={() => setCreating(false)}
                    onAdded={(message) => {
                        setCreating(false);
                        onToast(message);
                        void load();
                    }}
                />
            ) : null}
        </div>
    );
}

function AddLogin({
    onClose,
    onAdded,
}: {
    onClose: () => void;
    onAdded: (message: string) => void;
}) {
    const [accounts, setAccounts] = useState<Account[]>([]);
    const [domains, setDomains] = useState<DomainDetail[]>([]);
    const [username, setUsername] = useState('');
    const [domainId, setDomainId] = useState('');
    const [name, setName] = useState('');
    const [directory, setDirectory] = useState('');
    const [quota, setQuota] = useState('');
    const [fields, setFields] = useState<Record<string, string>>({});
    const [error, setError] = useState('');
    const [busy, setBusy] = useState(false);

    useEffect(() => {
        api<{ accounts: Account[] }>('/accounts?limit=200').then((response) => {
            setAccounts(response.accounts);
            const first = response.accounts[0];

            if (first) {
                setUsername(first.username);
            }
        });
    }, []);

    useEffect(() => {
        if (!username) {
            return;
        }

        api<{ domains: DomainDetail[] }>(`/domains?q=${encodeURIComponent(username)}`).then((response) => {
            const own = response.domains.filter((domain) => domain.username === username);
            setDomains(own);
            setDomainId(own[0] ? String(own[0].id) : '');
        });
    }, [username]);

    async function save(event: FormEvent): Promise<void> {
        event.preventDefault();
        setBusy(true);
        setError('');
        setFields({});

        try {
            const answer = await api<{ login: string; password: string }>('/ftp', {
                method: 'POST',
                body: {
                    username,
                    name,
                    domainId: Number(domainId),
                    directory,
                    quotaMb: quota === '' ? null : Number(quota),
                },
            });

            onAdded(`${answer.login} created. Password: ${answer.password}`);
        } catch (thrown) {
            setError(errorMessage(thrown));
            setFields((thrown as { fields?: Record<string, string> }).fields ?? {});
        } finally {
            setBusy(false);
        }
    }

    return (
        <Drawer
            title="New FTP login"
            subtitle="The password is generated and shown once."
            onClose={onClose}
            footer={
                <>
                    <Button onClick={onClose}>Cancel</Button>
                    <Button variant="primary" type="submit" form="ftp-form" busy={busy}>
                        Create login
                    </Button>
                </>
            }
        >
            {error ? <div className="banner bad">{error}</div> : null}

            <form id="ftp-form" className="form" onSubmit={(event) => void save(event)}>
                <label className="field">
                    <span className="field-label">Account</span>
                    <select className="input" value={username} onChange={(event) => setUsername(event.target.value)}>
                        {accounts.map((account) => (
                            <option key={account.id} value={account.username}>
                                {account.username}
                            </option>
                        ))}
                    </select>
                </label>

                <label className="field">
                    <span className="field-label">Domain</span>
                    <select className="input" value={domainId} onChange={(event) => setDomainId(event.target.value)}>
                        {domains.map((domain) => (
                            <option key={domain.id} value={domain.id}>
                                {domain.name}
                            </option>
                        ))}
                    </select>
                </label>

                <div className="grid-2 tight">
                    <Field
                        label="Login name"
                        value={name}
                        onChange={(event) => setName(event.target.value.toLowerCase())}
                        placeholder="upload"
                        hint="The login becomes name@domain"
                        error={fields.name}
                        required
                    />
                    <Field
                        label="Quota (MB)"
                        value={quota}
                        onChange={(event) => setQuota(event.target.value)}
                        placeholder="Blank for none"
                    />
                </div>

                <Field
                    label="Folder"
                    value={directory}
                    onChange={(event) => setDirectory(event.target.value)}
                    placeholder="public_html"
                    hint="Relative to the account's home; blank means the home itself"
                    error={fields.directory}
                />
            </form>
        </Drawer>
    );
}
