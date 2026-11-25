import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';

import { api } from '../api';
import { Badge, Button, Card, CardHead, Empty, errorMessage, Field, SkeletonRows } from '../ui';

interface SSHKey {
    ID: number;
    Name: string;
    Fingerprint: string;
    PublicKey: string;
    CreatedAt: string;
}

interface SSHAnswer {
    account: string;
    access: { allowed: boolean; enabled: boolean; active: boolean; pending: boolean; error: string | null };
    connection: { host: string; port: number; username: string; home: string; passwordSet: boolean };
    keys: SSHKey[] | null;
    maxKeys: number;
}

/** SSH and SFTP for one hosting account. The shell is a package feature; the
 *  keys work for both the shell and SFTP. */
export function Ssh({
    username,
    onToast,
}: {
    username?: string;
    onToast: (message: string, tone?: 'ok' | 'bad') => void;
}) {
    const [page, setPage] = useState<SSHAnswer | null>(null);
    const [error, setError] = useState('');
    const [busy, setBusy] = useState('');
    const [adding, setAdding] = useState(false);
    const [keyError, setKeyError] = useState('');

    const load = useCallback(async () => {
        try {
            const query = username ? `?username=${encodeURIComponent(username)}` : '';
            setPage(await api<SSHAnswer>(`/ssh${query}`));
            setError('');
        } catch (thrown) {
            setError(errorMessage(thrown));
        }
    }, [username]);

    useEffect(() => {
        void load();
    }, [load]);

    async function toggle(): Promise<void> {
        if (!page) {
            return;
        }

        setBusy('access');

        try {
            await api('/ssh', {
                method: 'PATCH',
                body: { username: page.account, enabled: !page.access.enabled },
            });
            onToast(page.access.enabled ? 'SSH is being turned off' : 'SSH is being turned on');
            await load();
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy('');
        }
    }

    async function addKey(event: FormEvent<HTMLFormElement>): Promise<void> {
        event.preventDefault();

        const form = event.currentTarget;
        const data = new FormData(form);
        setBusy('key');
        setKeyError('');

        try {
            await api('/ssh/keys', {
                method: 'POST',
                body: {
                    username: page?.account,
                    name: data.get('name'),
                    publicKey: data.get('publicKey'),
                },
            });
            onToast('The key is being added');
            form.reset();
            setAdding(false);
            await load();
        } catch (thrown) {
            setKeyError(errorMessage(thrown));
        } finally {
            setBusy('');
        }
    }

    async function remove(key: SSHKey): Promise<void> {
        if (!window.confirm(`Remove the key ${key.Name}?`)) {
            return;
        }

        setBusy(key.Fingerprint);

        try {
            await api(`/ssh/keys/${key.ID}`, { method: 'DELETE' });
            onToast(`${key.Name} removed`);
            await load();
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy('');
        }
    }

    if (error) {
        return (
            <div className="page">
                <header className="page-head">
                    <div>
                        <h1 className="page-title">SSH</h1>
                    </div>
                </header>
                <div className="banner bad">{error}</div>
            </div>
        );
    }

    if (!page) {
        return (
            <div className="page">
                <header className="page-head">
                    <div>
                        <h1 className="page-title">SSH</h1>
                    </div>
                </header>
                <SkeletonRows rows={3} />
            </div>
        );
    }

    const { access, connection } = page;
    const keys = page.keys ?? [];

    return (
        <div className="page">
            <header className="page-head">
                <div>
                    <h1 className="page-title">SSH</h1>
                    <p className="page-sub">
                        {page.account} · SFTP always works, a shell needs a package that includes it
                    </p>
                </div>
                <Button onClick={() => setAdding(true)}>Add a key</Button>
            </header>

            <div className="grid-2">
                <Card>
                    <CardHead title="Shell access">
                        <Badge tone={access.active ? 'ok' : access.enabled ? 'warn' : 'plain'}>
                            {access.active ? 'On' : access.enabled ? 'Requested' : 'Off'}
                        </Badge>
                    </CardHead>
                    <div className="card-body">
                        <dl className="kv">
                            <dt>In the package</dt>
                            <dd>{access.allowed ? 'Yes' : 'No'}</dd>
                            <dt>Turned on</dt>
                            <dd>{access.enabled ? 'Yes' : 'No'}</dd>
                        </dl>
                        {access.pending ? (
                            <p className="muted" style={{ marginBottom: 0 }}>
                                The server is applying the last change.
                            </p>
                        ) : null}
                        {access.error ? (
                            <p className="field-error" style={{ marginBottom: 0 }}>
                                {access.error}
                            </p>
                        ) : null}
                        <div className="row" style={{ marginTop: 12 }}>
                            <Button
                                variant={access.enabled ? 'ghost' : 'primary'}
                                onClick={() => void toggle()}
                                busy={busy === 'access'}
                                disabled={!access.allowed && !access.enabled}
                            >
                                {access.enabled ? 'Turn SSH off' : 'Turn SSH on'}
                            </Button>
                        </div>
                        {!access.allowed ? (
                            <p className="muted" style={{ marginBottom: 0, fontSize: 12 }}>
                                This account's package does not include SSH. Ask for a package that does.
                            </p>
                        ) : null}
                    </div>
                </Card>

                <Card>
                    <CardHead title="Connection" />
                    <div className="card-body">
                        <dl className="kv">
                            <dt>Host</dt>
                            <dd className="mono">{connection.host}</dd>
                            <dt>Port</dt>
                            <dd className="mono">{connection.port}</dd>
                            <dt>User</dt>
                            <dd className="mono">{connection.username}</dd>
                            <dt>Home</dt>
                            <dd className="mono">{connection.home}</dd>
                            <dt>SFTP password</dt>
                            <dd>{connection.passwordSet ? 'Set' : 'Not reset yet'}</dd>
                        </dl>
                        <p className="muted" style={{ marginBottom: 0, fontSize: 12 }}>
                            Signs in with the SFTP password or any key below. A shell, when the package
                            has one, runs inside the account's own home.
                        </p>
                    </div>
                </Card>
            </div>

            <Card>
                <CardHead title="Keys">
                    <span className="muted">
                        {keys.length} of {page.maxKeys}
                    </span>
                </CardHead>
                {keys.length === 0 ? (
                    <div className="card-body">
                        <Empty title="No keys yet">
                            Add the public half of a key you made with{' '}
                            <code>ssh-keygen -t ed25519</code>. Never paste a private key.
                        </Empty>
                    </div>
                ) : (
                    <table className="table">
                        <thead>
                            <tr>
                                <th>Name</th>
                                <th>Fingerprint</th>
                                <th>Added</th>
                                <th aria-label="Actions" />
                            </tr>
                        </thead>
                        <tbody>
                            {keys.map((key) => (
                                <tr key={key.ID}>
                                    <td>{key.Name}</td>
                                    <td className="mono muted">{key.Fingerprint}</td>
                                    <td className="muted">
                                        {key.CreatedAt
                                            ? new Date(key.CreatedAt).toLocaleDateString()
                                            : '—'}
                                    </td>
                                    <td className="row-end">
                                        <Button
                                            variant="ghost"
                                            busy={busy === key.Fingerprint}
                                            onClick={() => void remove(key)}
                                        >
                                            Remove
                                        </Button>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                )}
            </Card>

            {adding ? (
                <div className="overlay" role="dialog" aria-modal="true" aria-label="Add an SSH key">
                    <div className="drawer">
                        <header className="drawer-head">
                            <h2 className="drawer-title">Add a key</h2>
                            <Button variant="ghost" onClick={() => setAdding(false)}>
                                Close
                            </Button>
                        </header>
                        <form className="form" onSubmit={(event) => void addKey(event)}>
                            <Field label="Name" name="name" placeholder="laptop" required hint="So you can tell your keys apart" />
                            <div className="field span-2">
                                <label className="field-label" htmlFor="ssh-public-key">
                                    Public key
                                </label>
                                <textarea
                                    id="ssh-public-key"
                                    name="publicKey"
                                    className="input mono"
                                    style={{ minHeight: 120 }}
                                    placeholder="ssh-ed25519 AAAAC3Nza... you@laptop"
                                    required
                                    spellCheck={false}
                                />
                                {keyError ? <p className="field-error">{keyError}</p> : null}
                            </div>
                            <div className="row" style={{ justifyContent: 'flex-end' }}>
                                <Button variant="primary" type="submit" busy={busy === 'key'}>
                                    Add the key
                                </Button>
                            </div>
                        </form>
                    </div>
                </div>
            ) : null}
        </div>
    );
}
