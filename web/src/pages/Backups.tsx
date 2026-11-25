import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';

import { api, type DatabaseRow, type FileEntry, when } from '../api';
import { linkProps } from '../router';
import { Badge, Button, Card, CardHead, Empty, errorMessage, Field, SkeletonRows } from '../ui';

// Backups are archives in the account's own backups folder. The panel lists
// that folder through the agent rather than keeping a second ledger, so the
// list is always what is really on disk.
export function Backups({ username, onToast }: { username: string; onToast: (message: string, tone?: 'ok' | 'bad') => void }) {
    const [entries, setEntries] = useState<FileEntry[] | null>(null);
    const [databases, setDatabases] = useState<DatabaseRow[]>([]);
    const [error, setError] = useState('');
    const [busy, setBusy] = useState('');
    const [files, setFiles] = useState(true);
    const [email, setEmail] = useState(false);
    const [selected, setSelected] = useState<number[]>([]);
    const [name, setName] = useState('');

    const load = useCallback(async () => {
        try {
            const answer = await api<{ entries: FileEntry[] }>(
                `/accounts/${encodeURIComponent(username)}/backups`,
            );

            setEntries((answer.entries ?? []).filter((entry) => entry.type !== 'dir'));
            setError('');
        } catch (thrown) {
            setError(errorMessage(thrown));
            setEntries([]);
        }

        try {
            const list = await api<{ databases: DatabaseRow[] }>('/databases');
            setDatabases(list.databases.filter((database) => database.username === username));
        } catch {
            setDatabases([]);
        }
    }, [username]);

    useEffect(() => {
        void load();
    }, [load]);

    const chosen = databases.filter((_, index) => selected.includes(index)).map((database) => database.name);

    async function start(event: FormEvent): Promise<void> {
        event.preventDefault();
        setBusy('create');

        try {
            await api(`/accounts/${encodeURIComponent(username)}/backups`, {
                method: 'POST',
                body: { name, files, email, databases: chosen },
            });

            setName('');
            onToast('The backup is being written');
            window.setTimeout(() => void load(), 2500);
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy('');
        }
    }

    async function restore(entry: FileEntry): Promise<void> {
        if (!window.confirm(`Restore ${entry.name} over the current files?`)) {
            return;
        }

        setBusy(entry.name);

        try {
            await api(`/accounts/${encodeURIComponent(username)}/backups/restore`, {
                method: 'POST',
                body: { name: entry.name, files: true, email: false, databases: [] },
            });

            onToast('The restore is queued');
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy('');
        }
    }

    async function remove(entry: FileEntry): Promise<void> {
        if (!window.confirm(`Delete ${entry.name}?`)) {
            return;
        }

        setBusy(entry.name);

        try {
            await api(`/accounts/${encodeURIComponent(username)}/backups?name=${encodeURIComponent(entry.name)}`, {
                method: 'DELETE',
            });

            onToast('The archive was deleted');
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
                    <div className="crumbs">
                        <a {...linkProps(`/accounts/${username}`)}>{username}</a>
                        <span>/</span>
                        <span>backups</span>
                    </div>
                    <h1 className="page-title">Backups</h1>
                    <p className="page-sub">
                        {entries === null ? 'Reading the backups folder' : `${entries.length} archive${entries.length === 1 ? '' : 's'}`}
                    </p>
                </div>
                <Button onClick={() => void load()}>Refresh</Button>
            </header>

            {error ? <div className="banner bad">{error}</div> : null}

            <Card>
                <CardHead title="New backup" />
                <form className="card-body form" onSubmit={(event) => void start(event)}>
                    <div className="grid-2 tight">
                        <Field
                            label="Archive name"
                            value={name}
                            onChange={(event) => setName(event.target.value)}
                            placeholder="Blank for a dated name"
                        />
                        <div className="field">
                            <span className="field-label">Include</span>
                            <div className="stack" style={{ gap: 6 }}>
                                <label className="check">
                                    <input type="checkbox" checked={files} onChange={(event) => setFiles(event.target.checked)} />
                                    Files in the home directory
                                </label>
                                <label className="check">
                                    <input type="checkbox" checked={email} onChange={(event) => setEmail(event.target.checked)} />
                                    Mail
                                </label>
                                {databases.map((database, index) => (
                                    <label key={database.id} className="check">
                                        <input
                                            type="checkbox"
                                            checked={selected.includes(index)}
                                            onChange={(event) => {
                                                setSelected((current) =>
                                                    event.target.checked
                                                        ? [...current, index]
                                                        : current.filter((value) => value !== index),
                                                );
                                            }}
                                        />
                                        Database <span className="mono">{database.name}</span>
                                    </label>
                                ))}
                            </div>
                        </div>
                    </div>

                    <div className="row" style={{ justifyContent: 'flex-end' }}>
                        <Button type="submit" variant="primary" busy={busy === 'create'}>
                            Start backup
                        </Button>
                    </div>
                </form>
            </Card>

            <Card>
                <CardHead title="Archives" />
                <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Archive</th>
                                <th className="num">Size</th>
                                <th>Written</th>
                                <th style={{ textAlign: 'right' }}>Actions</th>
                            </tr>
                        </thead>

                        {entries === null ? (
                            <SkeletonRows rows={3} columns={4} />
                        ) : entries.length === 0 ? null : (
                            <tbody>
                                {entries.map((entry) => (
                                    <tr key={entry.name}>
                                        <td className="mono strong">{entry.name}</td>
                                        <td className="num">{(entry.size / (1024 * 1024)).toFixed(1)} MB</td>
                                        <td className="muted">{when(new Date(entry.modified * 1000).toISOString())}</td>
                                        <td>
                                            <div className="row" style={{ justifyContent: 'flex-end', gap: 6 }}>
                                                <Button size="sm" busy={busy === entry.name} onClick={() => void restore(entry)}>
                                                    Restore
                                                </Button>
                                                <Button size="sm" busy={busy === entry.name} onClick={() => void remove(entry)}>
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

                {entries !== null && entries.length === 0 ? (
                    <Empty title="No backups yet">
                        The first archive appears here once the server has written it. Restoring is queued, so the page
                        stays responsive.
                    </Empty>
                ) : null}
            </Card>

            <p className="muted" style={{ fontSize: 12 }}>
                <Badge tone="info">note</Badge> Archives live in <span className="mono">/home/{username}/backups</span>.
            </p>
        </div>
    );
}
