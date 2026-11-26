import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';

import { api, when, type DNSRecord, type DomainDetail as Domain } from '../api';
import { linkProps } from '../router';
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

const RECORD_TYPES = ['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'NS', 'SRV', 'CAA'];

export function DomainDetail({
    id,
    onToast,
}: {
    id: number;
    onToast: (message: string, tone?: 'ok' | 'bad') => void;
}) {
    const [domain, setDomain] = useState<Domain | null>(null);
    const [records, setRecords] = useState<DNSRecord[] | null>(null);
    const [error, setError] = useState('');
    const [busy, setBusy] = useState('');
    const [editing, setEditing] = useState<DNSRecord | 'new' | null>(null);

    const load = useCallback(async () => {
        try {
            const detail = await api<Domain>(`/domains/${id}`);
            setDomain(detail);
            setError('');

            try {
                const dns = await api<{ records: DNSRecord[] }>(`/domains/${id}/dns`);
                setRecords(dns.records);
            } catch {
                // A domain without a zone (a subdomain, for instance) is not an
                // error: its records live in the parent zone.
                setRecords([]);
            }
        } catch (thrown) {
            setError(errorMessage(thrown));
        }
    }, [id]);

    useEffect(() => {
        void load();
    }, [load]);

    async function issueCertificate(remove = false): Promise<void> {
        setBusy('ssl');

        try {
            await api(`/domains/${id}/ssl`, { method: remove ? 'DELETE' : 'POST', body: {} });
            onToast(remove ? 'The certificate will be removed' : 'A certificate will be issued');
            await load();
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy('');
        }
    }

    async function saveSettings(form: { phpVersion: string; forceHttps: boolean; documentRoot: string }): Promise<void> {
        setBusy('settings');

        try {
            await api(`/domains/${id}`, {
                method: 'PATCH',
                body: {
                    phpVersion: form.phpVersion,
                    forceHttps: form.forceHttps,
                    documentRoot: form.documentRoot,
                },
            });

            onToast('The website will be updated');
            await load();
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy('');
        }
    }

    async function removeRecord(record: DNSRecord): Promise<void> {
        setBusy(`dns-${record.id}`);

        try {
            await api(`/dns/${record.id}`, { method: 'DELETE' });
            onToast('The record was removed');
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
                        <h1 className="page-title">Domain</h1>
                        <p className="page-sub">{error}</p>
                    </div>
                </header>
                <a className="btn" {...linkProps('/domains')}>
                    Back to domains
                </a>
            </div>
        );
    }

    return (
        <div className="page">
            <header className="page-head">
                <div>
                    <div className="crumbs">
                        <a {...linkProps('/domains')}>Domains</a>
                        <span>/</span>
                        <span>{domain?.name ?? '…'}</span>
                    </div>
                    <h1 className="page-title mono">{domain?.name ?? 'Loading'}</h1>
                    <p className="page-sub">
                        {domain ? (
                            <>
                                Owned by <a {...linkProps(`/accounts/${domain.username}`)}>{domain.username}</a> ·{' '}
                                {domain.documentRoot || `/home/${domain.username}/public_html`}
                            </>
                        ) : (
                            'Reading the website'
                        )}
                    </p>
                </div>
                <div className="row">
                    <Badge tone={domain ? statusTone(domain.status) : 'plain'}>{domain?.status ?? '…'}</Badge>
                    <Button onClick={() => void issueCertificate(false)} busy={busy === 'ssl'} variant="primary">
                        {domain?.sslStatus === 'active' ? 'Renew certificate' : 'Get certificate'}
                    </Button>
                </div>
            </header>

            <div className="grid-2">
                <Card>
                    <CardHead title="Website">
                        <span className="muted" style={{ fontSize: 12 }}>
                            {domain?.phpVersion ? `PHP ${domain.phpVersion}` : 'Static files'}
                        </span>
                    </CardHead>

                    {domain === null ? (
                        <div className="card-body">
                            <div className="skeleton" style={{ height: 120 }} />
                        </div>
                    ) : (
                        <WebsiteForm domain={domain} busy={busy === 'settings'} onSave={saveSettings} />
                    )}
                </Card>

                <Card>
                    <CardHead title="Certificate">
                        <Badge tone={domain?.sslStatus === 'active' ? 'ok' : 'plain'}>{domain?.sslStatus || 'none'}</Badge>
                    </CardHead>

                    <div className="card-body">
                        {domain?.sslStatus === 'active' ? (
                            <>
                                <p className="muted">
                                    Issued for {domain.name} and expires {when(domain.sslExpiresAt)}. Renewal happens
                                    automatically; the button above forces it now.
                                </p>
                                <Button busy={busy === 'ssl'} onClick={() => void issueCertificate(true)}>
                                    Remove certificate
                                </Button>
                            </>
                        ) : (
                            <p className="muted">
                                {domain?.sslStatus
                                    ? `A certificate was requested but is ${domain.sslStatus}. The domain must point at `
                                    : 'No certificate yet. The button above asks for a free one; the domain must point at '}
                                {domain?.publicIp || 'this server'} first.
                            </p>
                        )}
                    </div>
                </Card>
            </div>

            <Card>
                <CardHead title="DNS records">
                    <Button size="sm" onClick={() => setEditing('new')} disabled={!domain?.zone}>
                        Add record
                    </Button>
                </CardHead>

                <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Name</th>
                                <th>Type</th>
                                <th>Value</th>
                                <th className="num">TTL</th>
                                <th style={{ textAlign: 'right' }}>Actions</th>
                            </tr>
                        </thead>

                        {records === null ? (
                            <SkeletonRows rows={4} columns={5} />
                        ) : records.length === 0 ? null : (
                            <tbody>
                                {records.map((record) => (
                                    <tr key={record.id}>
                                        <td className="mono">{record.name}</td>
                                        <td>
                                            <Badge tone="info">{record.type}</Badge>
                                        </td>
                                        <td className="mono" style={{ wordBreak: 'break-all' }}>
                                            {record.type === 'MX' && record.priority !== null
                                                ? `${record.priority} ${record.content}`
                                                : record.content}
                                        </td>
                                        <td className="num">{record.ttl}</td>
                                        <td>
                                            <div className="row" style={{ justifyContent: 'flex-end', gap: 6 }}>
                                                <Button size="sm" onClick={() => setEditing(record)}>
                                                    Edit
                                                </Button>
                                                <Button
                                                    size="sm"
                                                    busy={busy === `dns-${record.id}`}
                                                    onClick={() => void removeRecord(record)}
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

                {records !== null && records.length === 0 ? (
                    <Empty title={domain?.zone ? 'This zone has no records' : 'This domain has no zone'}>
                        {domain?.zone
                            ? 'Add an A record first: without one the domain does not resolve.'
                            : 'A subdomain keeps its records in the zone of the domain it belongs to.'}
                    </Empty>
                ) : null}
            </Card>

            {editing ? (
                <RecordDrawer
                    domainId={id}
                    record={editing === 'new' ? null : editing}
                    onClose={() => setEditing(null)}
                    onSaved={() => {
                        setEditing(null);
                        onToast('The record will be saved');
                        void load();
                    }}
                />
            ) : null}
        </div>
    );
}

function WebsiteForm({
    domain,
    busy,
    onSave,
}: {
    domain: Domain;
    busy: boolean;
    onSave: (form: { phpVersion: string; forceHttps: boolean; documentRoot: string }) => Promise<void>;
}) {
    const [phpVersion, setPHPVersion] = useState(domain.phpVersion ?? '');
    const [forceHttps, setForceHttps] = useState(Boolean(domain.sslStatus === 'active'));
    const [documentRoot, setDocumentRoot] = useState(domain.documentRoot ?? '');

    return (
        <form
            className="card-body form"
            onSubmit={(event: FormEvent) => {
                event.preventDefault();
                void onSave({ phpVersion, forceHttps, documentRoot });
            }}
        >
            <div className="grid-2 tight">
                <Field
                    label="PHP version"
                    value={phpVersion}
                    onChange={(event) => setPHPVersion(event.target.value)}
                    placeholder="8.3, or blank for static files"
                />
                <Field
                    label="Document root"
                    value={documentRoot}
                    onChange={(event) => setDocumentRoot(event.target.value)}
                    placeholder={`/home/${domain.username}/public_html`}
                />
            </div>

            <label className="check">
                <input type="checkbox" checked={forceHttps} onChange={(event) => setForceHttps(event.target.checked)} />
                Redirect all visitors to HTTPS
            </label>

            <div className="row" style={{ justifyContent: 'flex-end' }}>
                <Button type="submit" variant="primary" busy={busy}>
                    Save website
                </Button>
            </div>
        </form>
    );
}

function RecordDrawer({
    domainId,
    record,
    onClose,
    onSaved,
}: {
    domainId: number;
    record: DNSRecord | null;
    onClose: () => void;
    onSaved: () => void;
}) {
    const [name, setName] = useState(record?.name ?? '');
    const [type, setType] = useState(record?.type ?? 'A');
    const [content, setContent] = useState(record?.content ?? '');
    const [ttl, setTTL] = useState(String(record?.ttl ?? 3600));
    const [priority, setPriority] = useState(record?.priority === null || record?.priority === undefined ? '' : String(record.priority));
    const [fields, setFields] = useState<Record<string, string>>({});
    const [error, setError] = useState('');
    const [busy, setBusy] = useState(false);

    async function save(): Promise<void> {
        setBusy(true);
        setError('');
        setFields({});

        const body = {
            name,
            type,
            content,
            ttl: Number(ttl) || 3600,
            priority: priority === '' ? null : Number(priority),
        };

        try {
            if (record) {
                await api(`/dns/${record.id}`, { method: 'PATCH', body });
            } else {
                await api(`/domains/${domainId}/dns`, { method: 'POST', body });
            }

            onSaved();
        } catch (thrown) {
            setError(errorMessage(thrown));

            const apiError = thrown as { fields?: Record<string, string> };
            setFields(apiError.fields ?? {});
        } finally {
            setBusy(false);
        }
    }

    return (
        <Drawer
            title={record ? 'Edit record' : 'Add record'}
            subtitle="Changes are written to the zone and applied by the server agent."
            onClose={onClose}
            footer={
                <>
                    <Button onClick={onClose}>Cancel</Button>
                    <Button variant="primary" busy={busy} onClick={() => void save()}>
                        Save record
                    </Button>
                </>
            }
        >
            {error ? <div className="banner bad">{error}</div> : null}

            <div className="grid-2 tight">
                <Field
                    label="Name"
                    value={name}
                    onChange={(event) => setName(event.target.value)}
                    placeholder="@ for the domain itself"
                    error={fields.name}
                />
                <label className="field">
                    <span className="field-label">Type</span>
                    <select className="input" value={type} onChange={(event) => setType(event.target.value)}>
                        {RECORD_TYPES.map((candidate) => (
                            <option key={candidate} value={candidate}>
                                {candidate}
                            </option>
                        ))}
                    </select>
                    {fields.type ? <span className="field-error">{fields.type}</span> : null}
                </label>
            </div>

            <Field
                label="Value"
                value={content}
                onChange={(event) => setContent(event.target.value)}
                placeholder={type === 'A' ? '203.0.113.10' : type === 'MX' ? 'mail.example.com.' : 'The record content'}
                error={fields.content}
            />

            <div className="grid-2 tight">
                <Field label="TTL (seconds)" value={ttl} onChange={(event) => setTTL(event.target.value)} error={fields.ttl} />
                <Field
                    label="Priority"
                    value={priority}
                    onChange={(event) => setPriority(event.target.value)}
                    placeholder="Only for MX and SRV"
                    error={fields.priority}
                />
            </div>
        </Drawer>
    );
}
