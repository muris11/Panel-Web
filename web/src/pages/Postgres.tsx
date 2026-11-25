import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';

import { api, megabytes, type Account, type PostgresDatabase, type PostgresLogin } from '../api';
import { Badge, Button, Card, CardHead, Drawer, Empty, errorMessage, Field, SkeletonRows, statusTone } from '../ui';

/**
 * PostgreSQL is a separate module from MySQL on purpose. Its logins cannot take
 * a pre-hashed password, so a password is shown once and kept by the panel; the
 * agent sends it to the server when the login is applied.
 */
export function Postgres({ onToast }: { onToast: (message: string, tone?: 'ok' | 'bad') => void }) {
    const [databases, setDatabases] = useState<PostgresDatabase[] | null>(null);
    const [logins, setLogins] = useState<PostgresLogin[] | null>(null);
    const [accounts, setAccounts] = useState<Account[]>([]);
    const [error, setError] = useState('');
    const [adding, setAdding] = useState<'database' | 'login' | null>(null);
    const [editing, setEditing] = useState<PostgresLogin | null>(null);
    const [busy, setBusy] = useState('');

    const load = useCallback(async () => {
        try {
            const response = await api<{ databases: PostgresDatabase[]; users: PostgresLogin[] }>('/postgres');
            setDatabases(response.databases);
            setLogins(response.users);
            setError('');
        } catch (thrown) {
            setError(errorMessage(thrown));
        }
    }, []);

    useEffect(() => {
        void load();
        void api<{ accounts: Account[] }>('/accounts')
            .then((answer) => setAccounts(answer.accounts ?? []))
            .catch(() => setAccounts([]));
    }, [load]);

    async function removeDatabase(database: PostgresDatabase): Promise<void> {
        if (!window.confirm(`Drop ${database.name}? Its data goes with it.`)) {
            return;
        }

        setBusy(database.name);

        try {
            await api(`/postgres/${database.id}`, { method: 'DELETE' });
            onToast(`${database.name} will be dropped`);
            await load();
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy('');
        }
    }

    async function removeLogin(login: PostgresLogin): Promise<void> {
        if (!window.confirm(`Remove the PostgreSQL login ${login.name}?`)) {
            return;
        }

        setBusy(login.name);

        try {
            await api(`/postgres-users/${login.id}`, { method: 'DELETE' });
            onToast(`${login.name} removed`);
            await load();
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy('');
        }
    }

    async function rotate(login: PostgresLogin): Promise<void> {
        if (!window.confirm(`Give ${login.name} a new password?`)) {
            return;
        }

        setBusy(login.name);

        try {
            const grants = (databases ?? [])
                .filter((database) => login.databases.includes(database.name))
                .map((database) => database.id);

            const answer = await api<{ password?: string }>(`/postgres-users/${login.id}`, {
                method: 'PATCH',
                body: { databases: grants, password: '' },
            });

            onToast(answer.password ? `New password for ${login.name}: ${answer.password}` : `${login.name} reapplied`);
            await load();
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
                    <h1 className="page-title">PostgreSQL</h1>
                    <p className="page-sub">Databases and logins on the PostgreSQL cluster</p>
                </div>
                <div className="row">
                    <Button onClick={() => setAdding('login')}>New login</Button>
                    <Button variant="primary" onClick={() => setAdding('database')}>
                        New database
                    </Button>
                </div>
            </header>

            {error ? <div className="banner bad">{error}</div> : null}

            <Card>
                <CardHead title="Databases">
                    <Badge tone="plain">{databases?.length ?? 0}</Badge>
                </CardHead>

                {databases === null ? (
                    <SkeletonRows rows={2} />
                ) : databases.length === 0 ? (
                    <Empty title="No PostgreSQL databases yet">
                        <span className="muted">Create one and it appears here once the server has it.</span>
                    </Empty>
                ) : (
                    <div className="table-wrap">
                        <table>
                            <thead>
                                <tr>
                                    <th>Name</th>
                                    <th>Account</th>
                                    <th className="num">Size</th>
                                    <th>Status</th>
                                    <th />
                                </tr>
                            </thead>
                            <tbody>
                                {databases.map((database) => (
                                    <tr key={database.id}>
                                        <td className="mono">{database.name}</td>
                                        <td>{database.username}</td>
                                        <td className="num">
                                            {database.sizeBytes === null ? (
                                                <span className="muted">not measured</span>
                                            ) : (
                                                megabytes(database.sizeBytes)
                                            )}
                                        </td>
                                        <td>
                                            <Badge tone={statusTone(database.status)}>{database.status}</Badge>
                                        </td>
                                        <td className="actions">
                                            <Button
                                                variant="ghost"
                                                busy={busy === database.name}
                                                onClick={() => void removeDatabase(database)}
                                            >
                                                Drop
                                            </Button>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                )}
            </Card>

            <Card>
                <CardHead title="Logins">
                    <Badge tone="plain">{logins?.length ?? 0}</Badge>
                </CardHead>

                {logins === null ? (
                    <SkeletonRows rows={2} />
                ) : logins.length === 0 ? (
                    <Empty title="No PostgreSQL logins">
                        <span className="muted">A login is what an application connects with.</span>
                    </Empty>
                ) : (
                    <div className="table-wrap">
                        <table>
                            <thead>
                                <tr>
                                    <th>Login</th>
                                    <th>Account</th>
                                    <th>Databases</th>
                                    <th>Status</th>
                                    <th />
                                </tr>
                            </thead>
                            <tbody>
                                {logins.map((login) => (
                                    <tr key={login.id}>
                                        <td className="mono">{login.name}</td>
                                        <td>{login.username}</td>
                                        <td>
                                            {login.databases.length === 0 ? (
                                                <span className="muted">none</span>
                                            ) : (
                                                <span className="mono">{login.databases.join(', ')}</span>
                                            )}
                                        </td>
                                        <td>
                                            <Badge tone={statusTone(login.status)}>{login.status}</Badge>
                                        </td>
                                        <td className="actions">
                                            <Button variant="ghost" onClick={() => setEditing(login)}>
                                                Access
                                            </Button>
                                            <Button variant="ghost" busy={busy === login.name} onClick={() => void rotate(login)}>
                                                New password
                                            </Button>
                                            <Button variant="ghost" busy={busy === login.name} onClick={() => void removeLogin(login)}>
                                                Remove
                                            </Button>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                )}
            </Card>

            {adding ? (
                <AddPostgres
                    kind={adding}
                    accounts={accounts}
                    databases={databases ?? []}
                    onClose={() => setAdding(null)}
                    onAdded={(message) => {
                        setAdding(null);
                        onToast(message);
                        void load();
                    }}
                />
            ) : null}

            {editing ? (
                <EditAccess
                    login={editing}
                    databases={(databases ?? []).filter((database) => database.username === editing.username)}
                    onClose={() => setEditing(null)}
                    onSaved={(message) => {
                        setEditing(null);
                        onToast(message);
                        void load();
                    }}
                />
            ) : null}
        </div>
    );
}

function AddPostgres({
    kind,
    accounts,
    databases,
    onClose,
    onAdded,
}: {
    kind: 'database' | 'login';
    accounts: Account[];
    databases: PostgresDatabase[];
    onClose: () => void;
    onAdded: (message: string) => void;
}) {
    const [username, setUsername] = useState('');
    const [suffix, setSuffix] = useState('');
    const [password, setPassword] = useState('');
    const [grants, setGrants] = useState<number[]>([]);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState('');
    const [fields, setFields] = useState<Record<string, string>>({});

    useEffect(() => {
        if (accounts.length > 0) {
            setUsername((current) => current || accounts[0]?.username || '');
        }
    }, [accounts]);

    async function save(event: FormEvent): Promise<void> {
        event.preventDefault();
        setBusy(true);
        setError('');
        setFields({});

        try {
            if (kind === 'database') {
                await api('/postgres', { method: 'POST', body: { username, suffix } });
                onAdded(`${username}_${suffix} is being created`);
            } else {
                const answer = await api<{ password: string }>('/postgres-users', {
                    method: 'POST',
                    body: { username, suffix, password, databases: grants },
                });

                onAdded(`The login was created. Password: ${answer.password}`);
            }
        } catch (thrown) {
            setError(errorMessage(thrown));
            setFields((thrown as { fields?: Record<string, string> }).fields ?? {});
        } finally {
            setBusy(false);
        }
    }

    return (
        <Drawer
            title={kind === 'database' ? 'New PostgreSQL database' : 'New PostgreSQL login'}
            subtitle="The account name is prefixed, so siti plus shop becomes siti_shop."
            onClose={onClose}
            footer={
                <>
                    <Button onClick={onClose}>Cancel</Button>
                    <Button variant="primary" type="submit" form="postgres-form" busy={busy}>
                        {kind === 'database' ? 'Create database' : 'Create login'}
                    </Button>
                </>
            }
        >
            {error ? <div className="banner bad">{error}</div> : null}

            <form id="postgres-form" className="form" onSubmit={(event) => void save(event)}>
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

                <Field
                    label="Suffix"
                    value={suffix}
                    onChange={(event) => setSuffix(event.target.value)}
                    placeholder={kind === 'database' ? 'shop' : 'shopapp'}
                    hint={`Becomes ${username || 'account'}_${suffix || 'name'}`}
                    error={fields.suffix}
                    required
                />

                {kind === 'login' ? (
                    <>
                        <Field
                            label="Password"
                            type="text"
                            value={password}
                            onChange={(event) => setPassword(event.target.value)}
                            hint="Leave empty and the panel makes one for you."
                            error={fields.password}
                        />

                        <div className="field">
                            <span className="field-label">Databases this login may use</span>
                            <div className="stack" style={{ gap: 6 }}>
                                {databases.filter((database) => database.username === username).length === 0 ? (
                                    <span className="muted">This account has no PostgreSQL database yet.</span>
                                ) : (
                                    databases
                                        .filter((database) => database.username === username)
                                        .map((database) => (
                                            <label key={database.id} className="check">
                                                <input
                                                    type="checkbox"
                                                    checked={grants.includes(database.id)}
                                                    onChange={(event) => {
                                                        setGrants((current) =>
                                                            event.target.checked
                                                                ? [...current, database.id]
                                                                : current.filter((id) => id !== database.id),
                                                        );
                                                    }}
                                                />
                                                <span className="mono">{database.name}</span>
                                            </label>
                                        ))
                                )}
                            </div>
                        </div>
                    </>
                ) : null}
            </form>
        </Drawer>
    );
}

function EditAccess({
    login,
    databases,
    onClose,
    onSaved,
}: {
    login: PostgresLogin;
    databases: PostgresDatabase[];
    onClose: () => void;
    onSaved: (message: string) => void;
}) {
    const [grants, setGrants] = useState<number[]>(
        databases.filter((database) => login.databases.includes(database.name)).map((database) => database.id),
    );
    const [password, setPassword] = useState('');
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState('');
    const [fields, setFields] = useState<Record<string, string>>({});

    async function save(event: FormEvent): Promise<void> {
        event.preventDefault();
        setBusy(true);
        setError('');
        setFields({});

        try {
            const answer = await api<{ password?: string }>(`/postgres-users/${login.id}`, {
                method: 'PATCH',
                body: { databases: grants, password },
            });

            onSaved(answer.password ? `${login.name} now uses: ${answer.password}` : `${login.name} updated`);
        } catch (thrown) {
            setError(errorMessage(thrown));
            setFields((thrown as { fields?: Record<string, string> }).fields ?? {});
        } finally {
            setBusy(false);
        }
    }

    return (
        <Drawer
            title={`Access for ${login.name}`}
            subtitle="Leave the password empty to keep the current one."
            onClose={onClose}
            footer={
                <>
                    <Button onClick={onClose}>Cancel</Button>
                    <Button variant="primary" type="submit" form="postgres-access" busy={busy}>
                        Save
                    </Button>
                </>
            }
        >
            {error ? <div className="banner bad">{error}</div> : null}

            <form id="postgres-access" className="form" onSubmit={(event) => void save(event)}>
                <div className="field">
                    <span className="field-label">Databases</span>
                    <div className="stack" style={{ gap: 6 }}>
                        {databases.length === 0 ? (
                            <span className="muted">No database of this account exists.</span>
                        ) : (
                            databases.map((database) => (
                                <label key={database.id} className="check">
                                    <input
                                        type="checkbox"
                                        checked={grants.includes(database.id)}
                                        onChange={(event) => {
                                            setGrants((current) =>
                                                event.target.checked
                                                    ? [...current, database.id]
                                                    : current.filter((id) => id !== database.id),
                                            );
                                        }}
                                    />
                                    <span className="mono">{database.name}</span>
                                </label>
                            ))
                        )}
                    </div>
                </div>

                <Field
                    label="New password"
                    type="text"
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    hint="Empty keeps the password the application already uses."
                    error={fields.password}
                />
            </form>
        </Drawer>
    );
}
