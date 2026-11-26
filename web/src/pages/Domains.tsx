import { useCallback, useEffect, useMemo, useState } from 'react';
import type { FormEvent } from 'react';

import { api, when, type Account, type DomainDetail } from '../api';
import { linkProps } from '../router';
import { Badge, Button, Card, Drawer, Empty, errorMessage, Field, SkeletonRows, statusTone, TableFoot } from '../ui';

interface DomainRow extends DomainDetail {
    username: string;
}

// The list endpoint answers with the domains a caller may see, flattened with
// their owner, which is what this table shows.
export function Domains() {
    const [domains, setDomains] = useState<DomainRow[] | null>(null);
    const [error, setError] = useState('');
    const [search, setSearch] = useState('');
    const [publicIp, setPublicIp] = useState('');
    const [nameservers, setNameservers] = useState<string[]>([]);
    const [adding, setAdding] = useState(false);

    const load = useCallback(async () => {
        try {
            const response = await api<{ domains: DomainRow[]; publicIp: string; nameserver: string[] | null }>('/domains');
            setDomains(response.domains);
            setPublicIp(response.publicIp);
            setNameservers(response.nameserver ?? []);
            setError('');
        } catch (thrown) {
            setError(errorMessage(thrown));
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const visible = useMemo(() => {
        const needle = search.trim().toLowerCase();

        if (!needle) {
            return domains ?? [];
        }

        return (domains ?? []).filter(
            (domain) => domain.name.includes(needle) || domain.username.includes(needle),
        );
    }, [domains, search]);

    return (
        <div className="page">
            <header className="page-head">
                <div>
                    <h1 className="page-title">Domains</h1>
                    <p className="page-sub">
                        {domains === null
                            ? 'Loading'
                            : `${domains.length} website${domains.length === 1 ? '' : 's'}, each with its own zone`}
                    </p>
                </div>
                <div className="row">
                    <input
                        className="input"
                        style={{ width: 220 }}
                        placeholder="Search domains"
                        value={search}
                        onChange={(event) => setSearch(event.target.value)}
                        aria-label="Search domains"
                    />
                    <Button variant="primary" onClick={() => setAdding(true)}>
                        New website
                    </Button>
                </div>
            </header>

            {error ? <div className="banner bad">{error}</div> : null}

            {publicIp || nameservers.length > 0 ? (
                <div className="banner">
                    Point a domain's nameservers at {nameservers.length > 0 ? nameservers.join(' and ') : 'this server'}, or set an
                    A record to {publicIp || 'the server address'}.
                </div>
            ) : null}

            <Card>
                <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Domain</th>
                                <th>Account</th>
                                <th>PHP</th>
                                <th>TLS</th>
                                <th>Status</th>
                                <th>Added</th>
                                <th style={{ textAlign: 'right' }}>Actions</th>
                            </tr>
                        </thead>

                        {domains === null ? (
                            <SkeletonRows rows={5} columns={7} />
                        ) : visible.length === 0 ? null : (
                            <tbody>
                                {visible.map((domain) => (
                                    <tr key={domain.id}>
                                        <td>
                                            <a {...linkProps(`/domains/${domain.id}`)} className="cell-main">
                                                <span className="strong mono">{domain.name}</span>
                                                <span className="cell-sub">{domain.type}</span>
                                            </a>
                                        </td>
                                        <td className="mono">{domain.username}</td>
                                        <td>{domain.phpVersion ?? <span className="muted">none</span>}</td>
                                        <td>
                                            <Badge tone={domain.sslStatus === 'active' ? 'ok' : domain.sslStatus ? 'warn' : 'plain'}>
                                                {domain.sslStatus || 'no certificate'}
                                            </Badge>
                                        </td>
                                        <td>
                                            <Badge tone={statusTone(domain.status)}>{domain.status}</Badge>
                                        </td>
                                        <td className="muted">{when(domain.createdAt)}</td>
                                        <td>
                                            <div className="row" style={{ justifyContent: 'flex-end' }}>
                                                <a className="btn sm ghost" {...linkProps(`/domains/${domain.id}`)}>
                                                    Open
                                                </a>
                                            </div>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        )}
                    </table>
                </div>

                {domains !== null && visible.length === 0 ? (
                    <Empty title={search ? 'No domain matches that search' : 'No websites yet'}>
                        {search ? 'Try part of the domain or the account name.' : 'Add the first website to get started.'}
                    </Empty>
                ) : null}

                {domains !== null && domains.length > 0 ? (
                    <TableFoot>
                        <span>{domains.length} website{domains.length === 1 ? '' : 's'}</span>
                        <span className="sep" />
                        <span>{domains.filter((d) => d.sslStatus === 'active').length} with a certificate</span>
                        {search ? (
                            <>
                                <span className="sep" />
                                <span>{visible.length} matching the search</span>
                            </>
                        ) : null}
                    </TableFoot>
                ) : null}
            </Card>

            <p className="footnote">
                A domain belongs to one account, which owns its files, mail and databases.
            </p>

            {adding ? (
                <AddWebsite
                    onClose={() => setAdding(false)}
                    onAdded={() => {
                        setAdding(false);
                        void load();
                    }}
                />
            ) : null}
        </div>
    );
}

function AddWebsite({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
    const [accounts, setAccounts] = useState<Account[]>([]);
    const [username, setUsername] = useState('');
    const [name, setName] = useState('');
    const [kind, setKind] = useState('domain');
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
            await api('/domains', { method: 'POST', body: { username, name, type: kind } });
            onAdded();
        } catch (thrown) {
            setError(errorMessage(thrown));
            setFields((thrown as { fields?: Record<string, string> }).fields ?? {});
        } finally {
            setBusy(false);
        }
    }

    return (
        <Drawer
            title="New website"
            subtitle="The document root, nginx server block and DNS zone are created together."
            onClose={onClose}
            footer={
                <>
                    <Button onClick={onClose}>Cancel</Button>
                    <Button variant="primary" type="submit" form="website-form" busy={busy}>
                        Add website
                    </Button>
                </>
            }
        >
            {error ? <div className="banner bad">{error}</div> : null}

            <form id="website-form" className="form" onSubmit={(event) => void save(event)}>
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
                    label="Domain"
                    value={name}
                    onChange={(event) => setName(event.target.value.toLowerCase())}
                    placeholder="example.com"
                    hint="Lowercase letters, digits, dots and dashes. A wildcard starts with *."
                    error={fields.name}
                    required
                />

                <label className="field">
                    <span className="field-label">Type</span>
                    <select className="input" value={kind} onChange={(event) => setKind(event.target.value)}>
                        <option value="domain">Domain, with its own zone and mail</option>
                        <option value="subdomain">Subdomain of an existing zone</option>
                        <option value="pointer">Pointer to another site</option>
                    </select>
                    <span className="field-hint">A pointer shares another site's document root and serves it under this name.</span>
                </label>

                {accounts.length === 0 ? (
                    <p className="muted">Create an account first: a website needs an owner.</p>
                ) : null}
            </form>
        </Drawer>
    );
}

export type { Account };
