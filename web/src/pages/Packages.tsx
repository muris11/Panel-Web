import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';

import { ApiError, api } from '../api';
import { Badge, Button, Card, CardHead, Drawer, Empty, errorMessage, SkeletonRows } from '../ui';

interface Package {
    id: number;
    name: string;
    description: string;
    diskMb: number | null;
    inodes: number | null;
    bandwidthMb: number | null;
    domains: number | null;
    subdomains: number | null;
    pointers: number | null;
    emailAccounts: number | null;
    emailForwarders: number | null;
    databases: number | null;
    ftpAccounts: number | null;
    cronJobs: number | null;
    ssl: boolean;
    cgi: boolean;
    ssh: boolean;
    accounts: number;
}

interface PackagePage {
    packages: Package[];
    canEdit: boolean;
}

/** One optional limit in the editor. */
interface Field {
    key: keyof Package;
    label: string;
    hint: string;
}

/** The limits an account can run out of, grouped the way the form shows them. */
const GROUPS: { title: string; fields: Field[] }[] = [
    {
        title: 'Space',
        fields: [
            { key: 'diskMb', label: 'Disk space (MB)', hint: 'At least 1. Empty means no limit.' },
            { key: 'inodes', label: 'Inodes', hint: 'Files and directories. At least 1.' },
            { key: 'bandwidthMb', label: 'Monthly traffic (MB)', hint: 'Empty means no limit.' },
        ],
    },
    {
        title: 'Websites',
        fields: [
            { key: 'domains', label: 'Domains', hint: 'Empty means no limit.' },
            { key: 'subdomains', label: 'Subdomains', hint: 'Empty means no limit.' },
            { key: 'pointers', label: 'Pointers', hint: 'Empty means no limit.' },
        ],
    },
    {
        title: 'Mail and data',
        fields: [
            { key: 'emailAccounts', label: 'Mailboxes', hint: 'Empty means no limit.' },
            { key: 'emailForwarders', label: 'Forwarders', hint: 'Empty means no limit.' },
            { key: 'databases', label: 'MySQL databases', hint: 'Empty means no limit.' },
        ],
    },
    {
        title: 'Logins and jobs',
        fields: [
            { key: 'ftpAccounts', label: 'FTP logins', hint: 'Empty means no limit.' },
            { key: 'cronJobs', label: 'Cron jobs', hint: 'Empty means no limit.' },
        ],
    },
];

/** A number, or nothing when the limit is unlimited. */
function limitText(value: number | null): string {
    return value === null || value === undefined ? '' : String(value);
}

/** Hosting packages: what an account may have, and who is on each one. */
export function Packages({ role, onToast }: { role: string; onToast: (message: string, tone?: 'ok' | 'bad') => void }) {
    const [page, setPage] = useState<PackagePage | null>(null);
    const [error, setError] = useState('');
    const [editing, setEditing] = useState<Package | null>(null);
    const [creating, setCreating] = useState(false);
    const [form, setForm] = useState<Record<string, string>>({});
    const [name, setName] = useState('');
    const [description, setDescription] = useState('');
    const [flags, setFlags] = useState({ ssl: true, cgi: false, ssh: false });
    const [busy, setBusy] = useState(false);
    const [formError, setFormError] = useState('');
    const [fields, setFields] = useState<Record<string, string>>({});

    const load = useCallback(async () => {
        setError('');

        try {
            setPage(await api<PackagePage>('/packages'));
        } catch (problem) {
            setError(errorMessage(problem));
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const openEditor = (pkg: Package | null): void => {
        const next: Record<string, string> = {};

        for (const group of GROUPS) {
            for (const field of group.fields) {
                next[field.key] = pkg ? limitText(pkg[field.key] as number | null) : '';
            }
        }

        setForm(next);
        setName(pkg?.name ?? '');
        setDescription(pkg?.description ?? '');
        setFlags({ ssl: pkg?.ssl ?? true, cgi: pkg?.cgi ?? false, ssh: pkg?.ssh ?? false });
        setFields({});
        setFormError('');
        setEditing(pkg);
        setCreating(pkg === null);
    };

    const close = (): void => {
        setEditing(null);
        setCreating(false);
        setBusy(false);
    };

    const save = async (event: FormEvent): Promise<void> => {
        event.preventDefault();
        setBusy(true);
        setFormError('');
        setFields({});

        const body: Record<string, unknown> = { name, description, ...flags };

        for (const group of GROUPS) {
            for (const field of group.fields) {
                const raw = (form[field.key] ?? '').trim();
                body[field.key] = raw === '' ? null : Number(raw);
            }
        }

        try {
            if (editing) {
                await api(`/packages/${editing.id}`, { method: 'PATCH', body });
                onToast(`${name} saved.`);
            } else {
                await api('/packages', { method: 'POST', body });
                onToast(`${name} created.`);
            }

            close();
            await load();
        } catch (problem) {
            const message = errorMessage(problem);
            setFormError(message);

            if (problem instanceof ApiError && Object.keys(problem.fields).length > 0) {
                setFields(problem.fields);
            }
        } finally {
            setBusy(false);
        }
    };

    const remove = async (pkg: Package): Promise<void> => {
        setBusy(true);

        try {
            await api(`/packages/${pkg.id}`, { method: 'DELETE' });
            onToast(`${pkg.name} deleted.`);
            await load();
        } catch (problem) {
            onToast(errorMessage(problem), 'bad');
        } finally {
            setBusy(false);
        }
    };

    const open = editing !== null || creating;

    return (
        <>
            <header className="page-head">
                <div>
                    <h1>Packages</h1>
                    <p className="muted">
                        What an account is allowed. An empty limit means no limit at all.
                    </p>
                </div>
                {page?.canEdit ? (
                    <Button variant="primary" onClick={() => openEditor(null)}>
                        New package
                    </Button>
                ) : null}
            </header>

            {error ? (
                <p className="banner bad" role="alert">
                    {error}
                </p>
            ) : null}

            <Card>
                <CardHead title="Hosting packages">
                    {page ? <Badge tone="plain">{page.packages.length}</Badge> : null}
                </CardHead>

                {page === null && !error ? (
                    <SkeletonRows rows={3} />
                ) : page && page.packages.length === 0 ? (
                    <Empty title="No packages yet">
                        Create one, then put accounts on it so they have limits and a shell only if you mean it.
                    </Empty>
                ) : (
                    <div className="table-wrap">
                        <table className="table">
                            <thead>
                                <tr>
                                    <th>Name</th>
                                    <th>Disk</th>
                                    <th>Traffic</th>
                                    <th>Domains</th>
                                    <th>Mailboxes</th>
                                    <th>Features</th>
                                    <th className="num">Accounts</th>
                                    <th />
                                </tr>
                            </thead>
                            <tbody>
                                {(page?.packages ?? []).map((pkg) => (
                                    <tr key={pkg.id}>
                                        <td>
                                            <strong>{pkg.name}</strong>
                                            {pkg.description ? (
                                                <div className="muted small">{pkg.description}</div>
                                            ) : null}
                                        </td>
                                        <td>{pkg.diskMb === null ? 'no limit' : `${pkg.diskMb} MB`}</td>
                                        <td>{pkg.bandwidthMb === null ? 'no limit' : `${pkg.bandwidthMb} MB`}</td>
                                        <td>{pkg.domains === null ? 'no limit' : pkg.domains}</td>
                                        <td>{pkg.emailAccounts === null ? 'no limit' : pkg.emailAccounts}</td>
                                        <td>
                                            <span className="tags">
                                                {pkg.ssl ? <Badge tone="ok">SSL</Badge> : null}
                                                {pkg.cgi ? <Badge tone="plain">CGI</Badge> : null}
                                                {pkg.ssh ? <Badge tone="plain">SSH</Badge> : null}
                                            </span>
                                        </td>
                                        <td className="num">{pkg.accounts}</td>
                                        <td className="row end">
                                            {page?.canEdit ? (
                                                <>
                                                    <Button onClick={() => openEditor(pkg)}>Edit</Button>
                                                    <Button
                                                        variant="danger"
                                                        busy={busy}
                                                        disabled={pkg.accounts > 0}
                                                        title={
                                                            pkg.accounts > 0
                                                                ? 'Move its accounts to another package first'
                                                                : undefined
                                                        }
                                                        onClick={() => void remove(pkg)}
                                                    >
                                                        Delete
                                                    </Button>
                                                </>
                                            ) : null}
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                )}
            </Card>

            {role !== 'admin' ? (
                <p className="muted small">
                    These are your own packages. The limits you may hand out are the ones your reseller
                    package allows.
                </p>
            ) : null}

            {open ? (
                <Drawer
                    title={editing ? `Edit ${editing.name}` : 'New package'}
                    subtitle="Leave a limit empty to mean no limit."
                    onClose={close}
                    footer={
                        <>
                            <Button onClick={close}>Cancel</Button>
                            <Button variant="primary" busy={busy} onClick={(event) => void save(event)}>
                                {editing ? 'Save package' : 'Create package'}
                            </Button>
                        </>
                    }
                >
                    <form className="form" onSubmit={(event) => void save(event)}>
                        <label className="field-label" htmlFor="package-name">
                            Name
                        </label>
                        <input
                            id="package-name"
                            className="input"
                            value={name}
                            maxLength={64}
                            onChange={(event) => setName(event.target.value)}
                        />
                        {fields.name ? <p className="field-error">{fields.name}</p> : null}

                        <label className="field-label" htmlFor="package-description">
                            Description
                        </label>
                        <input
                            id="package-description"
                            className="input"
                            value={description}
                            maxLength={255}
                            onChange={(event) => setDescription(event.target.value)}
                        />
                        {fields.description ? <p className="field-error">{fields.description}</p> : null}

                        {GROUPS.map((group) => (
                            <div key={group.title}>
                                <span className="field-label">{group.title}</span>
                                <div className="grid-2">
                                    {group.fields.map((field) => (
                                        <div key={String(field.key)}>
                                            <label className="field-label" htmlFor={`package-${String(field.key)}`}>
                                                {field.label}
                                            </label>
                                            <input
                                                id={`package-${String(field.key)}`}
                                                className="input"
                                                inputMode="numeric"
                                                placeholder="no limit"
                                                value={form[String(field.key)] ?? ''}
                                                onChange={(event) =>
                                                    setForm((current) => ({
                                                        ...current,
                                                        [String(field.key)]: event.target.value,
                                                    }))
                                                }
                                            />
                                            {fields[String(field.key)] ? (
                                                <p className="field-error">{fields[String(field.key)]}</p>
                                            ) : (
                                                <p className="muted small">{field.hint}</p>
                                            )}
                                        </div>
                                    ))}
                                </div>
                            </div>
                        ))}

                        <span className="field-label">Features</span>
                        <div className="checks">
                            <label>
                                <input
                                    type="checkbox"
                                    checked={flags.ssl}
                                    onChange={() => setFlags({ ...flags, ssl: !flags.ssl })}
                                />
                                Free TLS certificates
                            </label>
                            <label>
                                <input
                                    type="checkbox"
                                    checked={flags.cgi}
                                    onChange={() => setFlags({ ...flags, cgi: !flags.cgi })}
                                />
                                CGI scripts, which run as the account
                            </label>
                            <label>
                                <input
                                    type="checkbox"
                                    checked={flags.ssh}
                                    onChange={() => setFlags({ ...flags, ssh: !flags.ssh })}
                                />
                                SSH shell in a jail of the account
                            </label>
                        </div>

                        {formError ? (
                            <p className="field-error" role="alert">
                                {formError}
                            </p>
                        ) : null}
                    </form>
                </Drawer>
            ) : null}
        </>
    );
}
