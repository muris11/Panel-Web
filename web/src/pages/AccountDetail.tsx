import { useCallback, useEffect, useState } from 'react';

import { api, megabytes, when, type Account, type Domain, type Task } from '../api';
import { linkProps, navigate } from '../router';
import {
    Badge,
    Button,
    Card,
    CardHead,
    Empty,
    errorMessage,
    SkeletonRows,
    statusTone,
} from '../ui';

interface DetailResponse {
    account: Account;
    domains: Domain[];
    tasks: Task[];
}

export function AccountDetail({
    username,
    onToast,
}: {
    username: string;
    onToast: (message: string, tone?: 'ok' | 'bad') => void;
}) {
    const [data, setData] = useState<DetailResponse | null>(null);
    const [error, setError] = useState('');
    const [busy, setBusy] = useState(false);
    const [confirming, setConfirming] = useState(false);
    const [typed, setTyped] = useState('');

    const load = useCallback(async () => {
        try {
            setData(await api<DetailResponse>(`/accounts/${encodeURIComponent(username)}`));
            setError('');
        } catch (thrown) {
            setError(errorMessage(thrown));
        }
    }, [username]);

    useEffect(() => {
        void load();
    }, [load]);

    async function toggleSuspend(): Promise<void> {
        if (!data) {
            return;
        }

        setBusy(true);

        try {
            if (data.account.status === 'suspended') {
                await api(`/accounts/${username}/unsuspend`, { method: 'POST', body: {} });
                onToast(`${username} will be unsuspended`);
            } else {
                const reason = window.prompt(`Why is ${username} being suspended?`, '') ?? '';
                await api(`/accounts/${username}/suspend`, { method: 'POST', body: { reason } });
                onToast(`${username} will be suspended`);
            }

            await load();
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy(false);
        }
    }

    async function remove(): Promise<void> {
        setBusy(true);

        try {
            await api(`/accounts/${username}?confirm=${encodeURIComponent(typed)}`, { method: 'DELETE' });
            onToast(`${username} is being deleted`);
            navigate('/accounts');
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
            setBusy(false);
        }
    }

    if (error) {
        return (
            <div className="page">
                <div className="banner bad">{error}</div>
                <a className="btn" {...linkProps('/accounts')}>
                    Back to accounts
                </a>
            </div>
        );
    }

    const account = data?.account;

    return (
        <div className="page">
            <header className="page-head">
                <div>
                    <a className="muted" {...linkProps('/accounts')} style={{ fontSize: 12 }}>
                        ← Accounts
                    </a>
                    <h1 className="page-title mono" style={{ marginTop: 6 }}>
                        {username}
                    </h1>
                    <p className="page-sub">
                        {account ? `${account.name} · ${account.email}` : 'Loading'}
                        {account?.package ? ` · ${account.package.name}` : ''}
                    </p>
                </div>

                <div className="row">
                    {account ? <Badge tone={statusTone(account.status)}>{account.status}</Badge> : null}
                    <Button onClick={() => void toggleSuspend()} busy={busy} disabled={!account}>
                        {account?.status === 'suspended' ? 'Unsuspend' : 'Suspend'}
                    </Button>
                    <Button variant="danger" onClick={() => setConfirming(true)} disabled={!account}>
                        Delete
                    </Button>
                </div>
            </header>

            <nav className="tabs">
                <a className="tab" {...linkProps(`/accounts/${username}/files`)}>
                    Files
                </a>
                <a className="tab" {...linkProps(`/accounts/${username}/cron`)}>
                    Cron
                </a>
                <a className="tab" {...linkProps(`/accounts/${username}/backups`)}>
                    Backups
                </a>
                <a className="tab" {...linkProps('/domains')}>
                    Domains
                </a>
                <a className="tab" {...linkProps('/mail')}>
                    Mail
                </a>
                <a className="tab" {...linkProps('/databases')}>
                    Databases
                </a>
                <a className="tab" {...linkProps('/ftp')}>
                    FTP
                </a>
            </nav>

            {account?.status === 'suspended' && account.reason ? (
                <div className="banner warn">Suspended: {account.reason}</div>
            ) : null}

            <div className="grid-2">
                <Card>
                    <CardHead title="Details" />
                    <div className="card-body">
                        <dl className="kv">
                            <dt>Linux user</dt>
                            <dd className="mono">{account?.uid ? `${username} (uid ${account.uid})` : username}</dd>
                            <dt>Owner</dt>
                            <dd>{account?.owner || 'administrator'}</dd>
                            <dt>Created</dt>
                            <dd>{when(account?.createdAt)}</dd>
                            <dt>Last sign in</dt>
                            <dd>{when(account?.lastLoginAt)}</dd>
                            <dt>Domains</dt>
                            <dd>{account?.domains ?? 0}</dd>
                        </dl>
                    </div>
                </Card>

                <Card>
                    <CardHead title="Package" />
                    <div className="card-body">
                        {account?.package ? (
                            <dl className="kv">
                                <dt>Name</dt>
                                <dd>{account.package.name}</dd>
                                <dt>Disk</dt>
                                <dd>{megabytes(account.package.diskMb)}</dd>
                                <dt>Bandwidth</dt>
                                <dd>{megabytes(account.package.bandwidthMb)}</dd>
                                <dt>Domains</dt>
                                <dd>{account.package.domains ?? 'unlimited'}</dd>
                                <dt>Databases</dt>
                                <dd>{account.package.databases ?? 'unlimited'}</dd>
                                <dt>Mailboxes</dt>
                                <dd>{account.package.emailAccounts ?? 'unlimited'}</dd>
                            </dl>
                        ) : (
                            <p className="muted" style={{ margin: 0 }}>
                                No package: nothing is limited by the panel. The server agent still applies the
                                system's own quotas.
                            </p>
                        )}
                    </div>
                </Card>
            </div>

            <Card>
                <CardHead title="Websites" />
                <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Domain</th>
                                <th>Type</th>
                                <th>PHP</th>
                                <th>SSL</th>
                                <th>Created</th>
                            </tr>
                        </thead>
                        {data === null ? (
                            <SkeletonRows rows={2} columns={5} />
                        ) : (
                            <tbody>
                                {(data.domains ?? []).map((domain) => (
                                    <tr key={domain.id}>
                                        <td>
                                            <a className="cell-main" {...linkProps(`/domains/${domain.id}`)}>
                                                <span className="strong">{domain.name}</span>
                                                {domain.documentRoot ? (
                                                    <span className="cell-sub mono">{domain.documentRoot}</span>
                                                ) : null}
                                            </a>
                                        </td>
                                        <td className="muted">{domain.type}</td>
                                        <td className="mono">{domain.phpVersion ?? '—'}</td>
                                        <td>
                                            <Badge tone={domain.sslStatus === 'active' ? 'ok' : 'plain'}>
                                                {domain.sslStatus || 'none'}
                                            </Badge>
                                        </td>
                                        <td className="muted">{when(domain.createdAt)}</td>
                                    </tr>
                                ))}
                            </tbody>
                        )}
                    </table>
                </div>
                {data && data.domains.length === 0 ? (
                    <Empty title="No website on this account">
                        Adding a domain creates its document root and its nginx server block.
                    </Empty>
                ) : null}
            </Card>

            <Card>
                <CardHead title="Recent server work" />
                <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Action</th>
                                <th>Status</th>
                                <th>When</th>
                            </tr>
                        </thead>
                        <tbody>
                            {(data?.tasks ?? []).map((task) => (
                                <tr key={task.id}>
                                    <td className="mono">{task.action}</td>
                                    <td>
                                        <Badge tone={statusTone(task.status)}>{task.status}</Badge>
                                    </td>
                                    <td className="muted">{when(task.createdAt)}</td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
                {data && data.tasks.length === 0 ? <Empty title="No work has run for this account yet" /> : null}
            </Card>

            {confirming ? (
                <div className="overlay" onMouseDown={(event) => event.target === event.currentTarget && setConfirming(false)}>
                    <div className="drawer" role="dialog" aria-modal="true" aria-label="Delete account">
                        <div className="drawer-head">
                            <div>
                                <div className="card-title">Delete {username}</div>
                                <p className="page-sub">This removes its files, databases and mail.</p>
                            </div>
                        </div>
                        <div className="drawer-body">
                            <div className="banner bad">
                                The account's home directory, databases and mailboxes are deleted on the server.
                                Backups already taken are kept.
                            </div>
                            <div className="field">
                                <label className="field-label" htmlFor="confirm-name">
                                    Type <span className="mono">{username}</span> to confirm
                                </label>
                                <input
                                    id="confirm-name"
                                    className="input"
                                    autoFocus
                                    value={typed}
                                    onChange={(event) => setTyped(event.target.value)}
                                    autoCapitalize="none"
                                    spellCheck={false}
                                />
                            </div>
                        </div>
                        <div className="drawer-foot">
                            <Button variant="ghost" onClick={() => setConfirming(false)}>
                                Cancel
                            </Button>
                            <Button variant="danger" disabled={typed !== username} busy={busy} onClick={() => void remove()}>
                                Delete account
                            </Button>
                        </div>
                    </div>
                </div>
            ) : null}
        </div>
    );
}
