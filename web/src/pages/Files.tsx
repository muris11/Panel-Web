import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';

import { api, type Account, type Content, type FileEntry, type Listing, when } from '../api';
import { linkProps } from '../router';
import { Badge, Button, Card, Drawer, Empty, errorMessage, Field, SkeletonRows } from '../ui';

// The file manager browses one account's home directory. Every call waits on
// the agent, because browsing has to answer immediately.
export function Files({ username, path }: { username: string; path: string }) {
    const [listing, setListing] = useState<Listing | null>(null);
    const [error, setError] = useState('');
    const [busy, setBusy] = useState('');
    const [editing, setEditing] = useState<Content | null>(null);
    const [creating, setCreating] = useState(false);

    const load = useCallback(async () => {
        if (!username) {
            return;
        }

        try {
            const answer = await api<Listing>(`/files?username=${encodeURIComponent(username)}&path=${encodeURIComponent(path)}`);
            setListing(answer);
            setError('');
        } catch (thrown) {
            setError(errorMessage(thrown));
            setListing({ path, entries: [], truncated: false });
        }
    }, [username, path]);

    useEffect(() => {
        void load();
    }, [load]);

    async function act(action: string, body: Record<string, unknown>): Promise<void> {
        setBusy(action);

        try {
            await api(`/files/${action}`, { method: 'POST', body: { username, ...body } });
            setError('');
            await load();
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy('');
        }
    }

    async function open(entry: FileEntry): Promise<void> {
        if (entry.type === 'dir') {
            window.location.href = `/files/${username}/${join(path, entry.name)}`;

            return;
        }

        setBusy(`open-${entry.name}`);

        try {
            const file = await api<Content>(
                `/files?username=${encodeURIComponent(username)}&path=${encodeURIComponent(join(path, entry.name))}&read=1`,
            );

            setEditing(file);
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy('');
        }
    }

    const crumb = path.split('/').filter(Boolean);

    return (
        <div className="page">
            <header className="page-head">
                <div>
                    <div className="crumbs">
                        <a {...linkProps(`/accounts/${username}`)}>{username}</a>
                        <span>/</span>
                        <span>files</span>
                    </div>
                    <h1 className="page-title mono">
                        {username}:/{path || ''}
                    </h1>
                    <p className="page-sub">
                        {listing === null ? 'Reading the folder' : `${listing.entries.length} entries`}
                        {listing?.truncated ? ' (truncated)' : ''}
                    </p>
                </div>
                <div className="row">
                    <Button onClick={() => setCreating(true)}>New folder</Button>
                    <Button
                        busy={busy === 'compress'}
                        onClick={() =>
                            void act('compress', { paths: [`${path || ''}/public_html`], destination: join(path, 'backup.zip'), format: 'zip' })
                        }
                    >
                        Compress public_html
                    </Button>
                </div>
            </header>

            {error ? <div className="banner bad">{error}</div> : null}

            <div className="row" style={{ flexWrap: 'wrap', gap: 4 }}>
                <a className="btn sm ghost" {...linkProps(`/files/${username}`)}>
                    home
                </a>
                {crumb.map((part, index) => (
                    <a
                        key={`${part}-${index}`}
                        className="btn sm ghost"
                        {...linkProps(`/files/${username}/${crumb.slice(0, index + 1).join('/')}`)}
                    >
                        {part}
                    </a>
                ))}
            </div>

            <Card>
                <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Name</th>
                                <th>Type</th>
                                <th className="num">Size</th>
                                <th>Mode</th>
                                <th>Changed</th>
                                <th style={{ textAlign: 'right' }}>Actions</th>
                            </tr>
                        </thead>

                        {listing === null ? (
                            <SkeletonRows rows={6} columns={6} />
                        ) : listing.entries.length === 0 ? null : (
                            <tbody>
                                {listing.entries.map((entry) => (
                                    <tr key={entry.name}>
                                        <td>
                                            <button className="link mono" onClick={() => void open(entry)} disabled={busy === `open-${entry.name}`}>
                                                {entry.name}
                                                {entry.type === 'dir' ? '/' : ''}
                                            </button>
                                        </td>
                                        <td>
                                            <Badge tone={entry.type === 'dir' ? 'info' : 'plain'}>{entry.type}</Badge>
                                        </td>
                                        <td className="num">{entry.type === 'dir' ? '—' : bytes(entry.size)}</td>
                                        <td className="mono">{entry.mode}</td>
                                        <td className="muted">{when(new Date(entry.modified * 1000).toISOString())}</td>
                                        <td>
                                            <div className="row" style={{ justifyContent: 'flex-end', gap: 6 }}>
                                                <Button
                                                    size="sm"
                                                    busy={busy === `delete-${entry.name}`}
                                                    onClick={() => {
                                                        if (!window.confirm(`Delete ${entry.name}?`)) {
                                                            return;
                                                        }

                                                        void act('delete', { paths: [join(path, entry.name)] });
                                                    }}
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

                {listing !== null && listing.entries.length === 0 ? (
                    <Empty title="This folder is empty">
                        Upload or create files through the file manager of the account.
                    </Empty>
                ) : null}
            </Card>

            {creating ? (
                <Drawer
                    title="New folder"
                    onClose={() => setCreating(false)}
                    footer={<Button onClick={() => setCreating(false)}>Done</Button>}
                >
                    <form
                        className="form"
                        onSubmit={(event: FormEvent<HTMLFormElement>) => {
                            event.preventDefault();

                            const data = new FormData(event.currentTarget);
                            const name = String(data.get('name') ?? '');

                            if (!name) {
                                return;
                            }

                            void act('mkdir', { path: join(path, name) });
                            setCreating(false);
                        }}
                    >
                        <Field label="Folder name" name="name" placeholder="assets" required autoFocus />
                        <div className="row" style={{ justifyContent: 'flex-end' }}>
                            <Button type="submit" variant="primary">
                                Create folder
                            </Button>
                        </div>
                    </form>
                </Drawer>
            ) : null}

            {editing ? (
                <Drawer
                    title={editing.path ?? 'File'}
                    subtitle={`${bytes(editing.size)} · version ${editing.version}`}
                    onClose={() => setEditing(null)}
                    footer={
                        <>
                            <Button onClick={() => setEditing(null)}>Close</Button>
                            <Button
                                variant="primary"
                                busy={busy === 'write'}
                                onClick={() =>
                                    void act('write', {
                                        path: editing.path ?? '',
                                        content: editing.content,
                                        version: editing.version,
                                    }).then(() => setEditing(null))
                                }
                            >
                                Save file
                            </Button>
                        </>
                    }
                >
                    <textarea
                        className="input mono"
                        style={{ minHeight: 420, lineHeight: 1.5 }}
                        value={editing.content}
                        onChange={(event) => setEditing({ ...editing, content: event.target.value })}
                        spellCheck={false}
                    />
                </Drawer>
            ) : null}
        </div>
    );
}

function join(base: string, name: string): string {
    return base ? `${base}/${name}` : name;
}

function bytes(size: number): string {
    if (size >= 1024 * 1024) {
        return `${(size / (1024 * 1024)).toFixed(1)} MB`;
    }

    if (size >= 1024) {
        return `${(size / 1024).toFixed(1)} kB`;
    }

    return `${size} B`;
}

/** The account picker, used when the URL has no account yet. */
export function FilesPicker() {
    const [accounts, setAccounts] = useState<Account[] | null>(null);

    useEffect(() => {
        api<{ accounts: Account[] }>('/accounts?limit=200').then((response) => setAccounts(response.accounts));
    }, []);

    return (
        <div className="page">
            <header className="page-head">
                <div>
                    <h1 className="page-title">Files</h1>
                    <p className="page-sub">Pick an account to browse its home directory</p>
                </div>
            </header>

            <Card>
                <div className="table-wrap">
                    <table>
                        <tbody>
                            {(accounts ?? []).map((account) => (
                                <tr key={account.id}>
                                    <td className="mono strong">{account.username}</td>
                                    <td className="muted">{account.name}</td>
                                    <td>
                                        <div className="row" style={{ justifyContent: 'flex-end' }}>
                                            <a className="btn sm" {...linkProps(`/files/${account.username}`)}>
                                                Browse
                                            </a>
                                        </div>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>

                {accounts !== null && accounts.length === 0 ? <Empty title="No accounts to browse" /> : null}
            </Card>
        </div>
    );
}
