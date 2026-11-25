import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';

import { api, megabytes, type Account, type DatabaseRow, type DatabaseUserRow } from '../api';
import { Badge, Button, Card, CardHead, Drawer, Empty, errorMessage, Field, SkeletonRows, statusTone } from '../ui';

export function Databases({ onToast }: { onToast: (message: string, tone?: 'ok' | 'bad') => void }) {
    const [databases, setDatabases] = useState<DatabaseRow[] | null>(null);
    const [users, setUsers] = useState<DatabaseUserRow[] | null>(null);
    const [error, setError] = useState('');
    const [adding, setAdding] = useState<'database' | 'user' | null>(null);
    const [busy, setBusy] = useState('');

    const load = useCallback(async () => {
        try {
            const response = await api<{ databases: DatabaseRow[]; users: DatabaseUserRow[] }>('/databases');
            setDatabases(response.databases);
            setUsers(response.users);
            setError('');
        } catch (thrown) {
            setError(errorMessage(thrown));
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    async function resetLogin(user: DatabaseUserRow): Promise<void> {
        if (!window.confirm(`Give ${user.name} a new password?`)) {
            return;
        }

        setBusy(user.name);

        try {
            // The grants have to travel with the request, or the login loses
            // access to the databases it was given.
            const grants = user.databases
                .map((name) => (databases ?? []).find((database) => database.name === name)?.id)
                .filter((id): id is number => typeof id === 'number');

            const answer = await api<{ password: string }>(`/database-users/${user.id}`, {
                method: 'PATCH',
                body: { databases: grants },
            });

            onToast(`New password for ${user.name}: ${answer.password}`);
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy('');
        }
    }

    async function removeLogin(user: DatabaseUserRow): Promise<void> {
        if (!window.confirm(`Remove the MySQL login ${user.name}?`)) {
            return;
        }

        setBusy(user.name);

        try {
            await api(`/database-users/${user.id}`, { method: 'DELETE' });
            onToast(`${user.name} removed`);
            await load();
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy('');
        }
    }

    async function removeDatabase(database: DatabaseRow): Promise<void> {
        if (!window.confirm(`Drop ${database.name}? Its data is deleted with it.`)) {
            return;
        }

        setBusy(database.name);

        try {
            await api(`/databases/${database.id}?confirm=${encodeURIComponent(database.name)}`, { method: 'DELETE' });
            onToast(`${database.name} will be dropped`);
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
                    <h1 className="page-title">Databases</h1>
                    <p className="page-sub">MySQL databases and the logins that reach them</p>
                </div>
                <div className="row">
                    <Button onClick={() => setAdding('user')}>New login</Button>
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

                <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Name</th>
                                <th>Account</th>
                                <th className="num">Size</th>
                                <th>Status</th>
                                <th style={{ textAlign: 'right' }}>Actions</th>
                            </tr>
                        </thead>

                        {databases === null ? (
                            <SkeletonRows rows={3} columns={5} />
                        ) : databases.length === 0 ? null : (
                            <tbody>
                                {databases.map((database) => (
                                    <tr key={database.id}>
                                        <td className="mono strong">{database.name}</td>
                                        <td className="mono">{database.username}</td>
                                        <td className="num">{megabytes(database.sizeBytes ? database.sizeBytes / (1024 * 1024) : null)}</td>
                                        <td>
                                            <Badge tone={statusTone(database.status)}>{database.status}</Badge>
                                        </td>
                                        <td>
                                            <div className="row" style={{ justifyContent: 'flex-end' }}>
                                                <Button
                                                    size="sm"
                                                    busy={busy === database.name}
                                                    onClick={() => void removeDatabase(database)}
                                                >
                                                    Drop
                                                </Button>
                                            </div>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        )}
                    </table>
                </div>

                {databases !== null && databases.length === 0 ? (
                    <Empty title="No databases yet">
                        A database is named after its account, which keeps two accounts from colliding.
                    </Empty>
                ) : null}
            </Card>

            <Card>
                <CardHead title="Logins">
                    <Badge tone="plain">{users?.length ?? 0}</Badge>
                </CardHead>

                <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Login</th>
                                <th>Account</th>
                                <th>Grants</th>
                                <th>Status</th>
                                <th style={{ textAlign: 'right' }}>Actions</th>
                            </tr>
                        </thead>
                        <tbody>
                            {(users ?? []).map((user) => (
                                <tr key={user.id}>
                                    <td className="mono strong">{user.name}</td>
                                    <td className="mono">{user.username}</td>
                                    <td className="muted mono">{user.databases.join(', ') || '—'}</td>
                                    <td>
                                        <Badge tone={statusTone(user.status)}>{user.status}</Badge>
                                    </td>
                                    <td>
                                        <div className="row" style={{ justifyContent: 'flex-end', gap: 6 }}>
                                            <Button size="sm" busy={busy === user.name} onClick={() => void resetLogin(user)}>
                                                New password
                                            </Button>
                                            <Button size="sm" busy={busy === user.name} onClick={() => void removeLogin(user)}>
                                                Delete
                                            </Button>
                                        </div>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>

                {users !== null && users.length === 0 ? (
                    <Empty title="No logins yet">A login is what an application uses to reach its database.</Empty>
                ) : null}
            </Card>

            {adding ? (
                <AddDatabase
                    kind={adding}
                    databases={databases ?? []}
                    onClose={() => setAdding(null)}
                    onAdded={(message) => {
                        setAdding(null);
                        onToast(message);
                        void load();
                    }}
                />
            ) : null}
        </div>
    );
}

function AddDatabase({
    kind,
    databases,
    onClose,
    onAdded,
}: {
    kind: 'database' | 'user';
    databases: DatabaseRow[];
    onClose: () => void;
    onAdded: (message: string) => void;
}) {
    const [accounts, setAccounts] = useState<Account[]>([]);
    const [username, setUsername] = useState('');
    const [suffix, setSuffix] = useState('');
    const [grants, setGrants] = useState<number[]>([]);
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

    async function save(event: FormEvent): Promise<void> {
        event.preventDefault();
        setBusy(true);
        setError('');
        setFields({});

        try {
            if (kind === 'database') {
                await api('/databases', { method: 'POST', body: { username, suffix } });
                onAdded(`${username}_${suffix} is being created`);
            } else {
                const answer = await api<{ password: string }>('/database-users', {
                    method: 'POST',
                    body: { username, suffix, databases: grants },
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
            title={kind === 'database' ? 'New database' : 'New database login'}
            subtitle={
                kind === 'database'
                    ? 'The name is prefixed with the account, so siti and toko becomes siti_toko.'
                    : 'The login gets a generated password, shown once.'
            }
            onClose={onClose}
            footer={
                <>
                    <Button onClick={onClose}>Cancel</Button>
                    <Button variant="primary" type="submit" form="database-form" busy={busy}>
                        {kind === 'database' ? 'Create database' : 'Create login'}
                    </Button>
                </>
            }
        >
            {error ? <div className="banner bad">{error}</div> : null}

            <form id="database-form" className="form" onSubmit={(event) => void save(event)}>
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
                    hint={kind === 'database' ? `Becomes ${username || 'account'}_${suffix || 'shop'}` : 'The login name'}
                    error={fields.suffix}
                    required
                />

                {kind === 'user' ? (
                    <div className="field">
                        <span className="field-label">Databases this login may use</span>
                        <div className="stack" style={{ gap: 6 }}>
                            {databases
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
                                ))}
                        </div>
                    </div>
                ) : null}
            </form>
        </Drawer>
    );
}
