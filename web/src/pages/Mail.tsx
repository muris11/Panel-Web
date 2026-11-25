import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';

import { api, type DomainDetail, type Forwarder, type Mailbox, type MailDomain } from '../api';
import {
    Badge,
    Button,
    Card,
    CardHead,
    Drawer,
    Empty,
    errorMessage,
    Field,
    SkeletonRows,
    statusTone,
} from '../ui';

export function Mail({ onToast }: { onToast: (message: string, tone?: 'ok' | 'bad') => void }) {
    const [domains, setDomains] = useState<MailDomain[] | null>(null);
    const [selected, setSelected] = useState<MailDomain | null>(null);
    const [error, setError] = useState('');
    const [adding, setAdding] = useState(false);

    const load = useCallback(async () => {
        try {
            const response = await api<{ domains: MailDomain[] }>('/mail');
            setDomains(response.domains);
            setError('');
        } catch (thrown) {
            setError(errorMessage(thrown));
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    return (
        <div className="page">
            <header className="page-head">
                <div>
                    <h1 className="page-title">Mail</h1>
                    <p className="page-sub">
                        {domains === null
                            ? 'Loading'
                            : `${domains.length} domain${domains.length === 1 ? '' : 's'} with mail service`}
                    </p>
                </div>
                <Button variant="primary" onClick={() => setAdding(true)}>
                    Add mail domain
                </Button>
            </header>

            {error ? <div className="banner bad">{error}</div> : null}

            <Card>
                <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Domain</th>
                                <th>Account</th>
                                <th className="num">Mailboxes</th>
                                <th className="num">Forwarders</th>
                                <th>DKIM</th>
                                <th>Status</th>
                                <th style={{ textAlign: 'right' }}>Actions</th>
                            </tr>
                        </thead>

                        {domains === null ? (
                            <SkeletonRows rows={4} columns={7} />
                        ) : domains.length === 0 ? null : (
                            <tbody>
                                {domains.map((domain) => (
                                    <tr key={domain.id}>
                                        <td className="mono strong">{domain.domainName}</td>
                                        <td className="mono">{domain.username}</td>
                                        <td className="num">{domain.mailboxes}</td>
                                        <td className="num">{domain.forwarders}</td>
                                        <td>
                                            <Badge tone={domain.dkimPublicKey ? 'ok' : 'warn'}>
                                                {domain.dkimPublicKey ? 'signed' : 'no key'}
                                            </Badge>
                                        </td>
                                        <td>
                                            <Badge tone={statusTone(domain.status)}>{domain.status}</Badge>
                                        </td>
                                        <td>
                                            <div className="row" style={{ justifyContent: 'flex-end' }}>
                                                <Button size="sm" onClick={() => setSelected(domain)}>
                                                    Manage
                                                </Button>
                                            </div>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        )}
                    </table>
                </div>

                {domains !== null && domains.length === 0 ? (
                    <Empty title="No mail domains yet">
                        Mail service is added per domain: pick the website that should receive mail, and DKIM, SPF and
                        MX records are written for you.
                    </Empty>
                ) : null}
            </Card>

            {adding ? (
                <AddMailDomain
                    onClose={() => setAdding(false)}
                    onAdded={() => {
                        setAdding(false);
                        onToast('Mail service is being set up');
                        void load();
                    }}
                />
            ) : null}

            {selected ? (
                <MailboxDrawer
                    domain={selected}
                    onClose={() => {
                        setSelected(null);
                        void load();
                    }}
                    onToast={onToast}
                />
            ) : null}
        </div>
    );
}

function AddMailDomain({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
    const [domains, setDomains] = useState<DomainDetail[]>([]);
    const [domainId, setDomainId] = useState('');
    const [error, setError] = useState('');
    const [busy, setBusy] = useState(false);

    useEffect(() => {
        api<{ domains: DomainDetail[] }>('/domains').then((response) => {
            setDomains(response.domains);
            const first = response.domains[0];

            if (first) {
                setDomainId(String(first.id));
            }
        });
    }, []);

    async function save(): Promise<void> {
        setBusy(true);
        setError('');

        try {
            await api('/mail', { method: 'POST', body: { domainId: Number(domainId) } });
            onAdded();
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy(false);
        }
    }

    return (
        <Drawer
            title="Add mail domain"
            subtitle="Mailboxes, DKIM and the MX record are created for the website you pick."
            onClose={onClose}
            footer={
                <>
                    <Button onClick={onClose}>Cancel</Button>
                    <Button variant="primary" busy={busy} onClick={() => void save()} disabled={!domainId}>
                        Add mail
                    </Button>
                </>
            }
        >
            {error ? <div className="banner bad">{error}</div> : null}

            <label className="field">
                <span className="field-label">Website</span>
                <select className="input" value={domainId} onChange={(event) => setDomainId(event.target.value)}>
                    {domains.map((domain) => (
                        <option key={domain.id} value={domain.id}>
                            {domain.name} ({domain.username})
                        </option>
                    ))}
                </select>
            </label>

            {domains.length === 0 ? <p className="muted">A website is needed before it can receive mail.</p> : null}
        </Drawer>
    );
}

function MailboxDrawer({
    domain,
    onClose,
    onToast,
}: {
    domain: MailDomain;
    onClose: () => void;
    onToast: (message: string, tone?: 'ok' | 'bad') => void;
}) {
    const [mailboxes, setMailboxes] = useState<Mailbox[] | null>(null);
    const [forwarders, setForwarders] = useState<Forwarder[] | null>(null);
    const [localPart, setLocalPart] = useState('');
    const [quota, setQuota] = useState('1024');
    const [forwardTo, setForwardTo] = useState('');
    const [error, setError] = useState('');
    const [busy, setBusy] = useState('');

    const load = useCallback(async () => {
        try {
            const [boxes, forwards] = await Promise.all([
                api<{ mailboxes: Mailbox[] }>(`/mail/${domain.id}/mailboxes`),
                api<{ forwarders: Forwarder[] }>(`/mail/${domain.id}/forwarders`),
            ]);

            setMailboxes(boxes.mailboxes);
            setForwarders(forwards.forwarders);
        } catch (thrown) {
            setError(errorMessage(thrown));
        }
    }, [domain.id]);

    useEffect(() => {
        void load();
    }, [load]);

    async function addMailbox(event: FormEvent): Promise<void> {
        event.preventDefault();
        setBusy('mailbox');

        try {
            const answer = await api<{ password: string }>(`/mail/${domain.id}/mailboxes`, {
                method: 'POST',
                body: { localPart, quotaMb: quota === '' ? null : Number(quota) },
            });

            setLocalPart('');
            onToast(`${localPart}@${domain.domainName} created. Password: ${answer.password}`);
            await load();
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy('');
        }
    }

    async function addForwarder(event: FormEvent): Promise<void> {
        event.preventDefault();
        setBusy('forwarder');

        try {
            await api(`/mail/${domain.id}/forwarders`, {
                method: 'POST',
                body: { localPart, destinations: [forwardTo] },
            });

            setForwardTo('');
            setLocalPart('');
            onToast('The forwarder is being set up');
            await load();
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy('');
        }
    }

    async function remove(path: string, label: string): Promise<void> {
        if (!window.confirm(`Remove ${label}?`)) {
            return;
        }

        setBusy(path);

        try {
            await api(path, { method: 'DELETE' });
            onToast(`${label} removed`);
            await load();
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy('');
        }
    }

    return (
        <Drawer
            title={domain.domainName}
            subtitle="Mailboxes store mail; forwarders send it somewhere else."
            onClose={onClose}
            footer={<Button onClick={onClose}>Done</Button>}
        >
            {error ? <div className="banner bad">{error}</div> : null}

            <Card>
                <CardHead title="Mailboxes">
                    <Badge tone="plain">{mailboxes?.length ?? 0}</Badge>
                </CardHead>

                <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Address</th>
                                <th className="num">Quota</th>
                                <th>Status</th>
                                <th style={{ textAlign: 'right' }}>Actions</th>
                            </tr>
                        </thead>
                        <tbody>
                            {(mailboxes ?? []).map((box) => (
                                <tr key={box.id}>
                                    <td className="mono">
                                        {box.localPart}@{domain.domainName}
                                    </td>
                                    <td className="num">{box.quotaMb ? `${box.quotaMb} MB` : 'unlimited'}</td>
                                    <td>
                                        <Badge tone={statusTone(box.status)}>{box.status}</Badge>
                                    </td>
                                    <td>
                                        <div className="row" style={{ justifyContent: 'flex-end' }}>
                                            <Button
                                                size="sm"
                                                busy={busy === `mailboxes/${box.id}`}
                                                onClick={() => void remove(`/mailboxes/${box.id}`, box.localPart)}
                                            >
                                                Delete
                                            </Button>
                                        </div>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>

                {mailboxes !== null && mailboxes.length === 0 ? (
                    <p className="muted" style={{ padding: '0 16px 12px' }}>
                        No mailbox yet. Create the first one below.
                    </p>
                ) : null}

                <form className="card-body form" onSubmit={(event) => void addMailbox(event)}>
                    <div className="grid-2 tight">
                        <Field
                            label="Address"
                            value={localPart}
                            onChange={(event) => setLocalPart(event.target.value)}
                            placeholder="hello"
                            required
                        />
                        <Field label="Quota (MB)" value={quota} onChange={(event) => setQuota(event.target.value)} />
                    </div>

                    <div className="row" style={{ justifyContent: 'flex-end' }}>
                        <Button type="submit" variant="primary" busy={busy === 'mailbox'}>
                            Create mailbox
                        </Button>
                    </div>
                </form>
            </Card>

            <Card>
                <CardHead title="Forwarders">
                    <Badge tone="plain">{forwarders?.length ?? 0}</Badge>
                </CardHead>

                <div className="table-wrap">
                    <table>
                        <tbody>
                            {(forwarders ?? []).map((forwarder) => (
                                <tr key={forwarder.id}>
                                    <td className="mono">
                                        {forwarder.localPart}@{domain.domainName}
                                    </td>
                                    <td className="muted mono">{forwarder.destinations.join(', ')}</td>
                                    <td>
                                        <div className="row" style={{ justifyContent: 'flex-end' }}>
                                            <Button
                                                size="sm"
                                                busy={busy === `forwarders/${forwarder.id}`}
                                                onClick={() => void remove(`/forwarders/${forwarder.id}`, forwarder.localPart)}
                                            >
                                                Delete
                                            </Button>
                                        </div>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>

                <form className="card-body form" onSubmit={(event) => void addForwarder(event)}>
                    <div className="grid-2 tight">
                        <Field
                            label="From"
                            value={localPart}
                            onChange={(event) => setLocalPart(event.target.value)}
                            placeholder="info"
                            required
                        />
                        <Field
                            label="To"
                            value={forwardTo}
                            onChange={(event) => setForwardTo(event.target.value)}
                            placeholder="you@elsewhere.test"
                            required
                        />
                    </div>

                    <div className="row" style={{ justifyContent: 'flex-end' }}>
                        <Button type="submit" busy={busy === 'forwarder'}>
                            Add forwarder
                        </Button>
                    </div>
                </form>
            </Card>
        </Drawer>
    );
}
