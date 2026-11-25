import { useCallback, useEffect, useMemo, useState } from 'react';
import type { FormEvent } from 'react';

import { api, ApiError, when, type Account, type Package } from '../api';
import { linkProps } from '../router';
import { useSession } from '../session';
import {
    Badge,
    Button,
    Card,
    Drawer,
    Empty,
    errorMessage,
    Field,
    IconPlus,
    SkeletonRows,
    statusTone,
} from '../ui';

export function Accounts({ onToast }: { onToast: (message: string, tone?: 'ok' | 'bad') => void }) {
    const { user } = useSession();

    const [accounts, setAccounts] = useState<Account[] | null>(null);
    const [error, setError] = useState('');
    const [search, setSearch] = useState('');
    const [creating, setCreating] = useState(false);
    const [busy, setBusy] = useState<string | null>(null);

    const load = useCallback(async () => {
        try {
            const response = await api<{ accounts: Account[] }>('/accounts?limit=200');
            setAccounts(response.accounts);
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
            return accounts ?? [];
        }

        return (accounts ?? []).filter(
            (account) =>
                account.username.includes(needle) ||
                account.name.toLowerCase().includes(needle) ||
                account.email.toLowerCase().includes(needle),
        );
    }, [accounts, search]);

    async function act(account: Account, action: 'suspend' | 'unsuspend'): Promise<void> {
        setBusy(account.username);

        try {
            const reason = action === 'suspend' ? window.prompt(`Why is ${account.username} being suspended?`, '') ?? '' : '';

            await api(`/accounts/${account.username}/${action}`, {
                method: 'POST',
                body: action === 'suspend' ? { reason } : {},
            });

            onToast(action === 'suspend' ? `${account.username} will be suspended` : `${account.username} will be unsuspended`);
            await load();
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy(null);
        }
    }

    return (
        <div className="page">
            <header className="page-head">
                <div>
                    <h1 className="page-title">Accounts</h1>
                    <p className="page-sub">
                        {accounts === null ? 'Loading' : `${accounts.length} hosting account${accounts.length === 1 ? '' : 's'}`}
                    </p>
                </div>
                <div className="row">
                    <input
                        className="input"
                        style={{ width: 220 }}
                        placeholder="Search accounts"
                        value={search}
                        onChange={(event) => setSearch(event.target.value)}
                        aria-label="Search accounts"
                    />
                    <Button variant="primary" onClick={() => setCreating(true)}>
                        <IconPlus /> New account
                    </Button>
                </div>
            </header>

            {error ? <div className="banner bad">{error}</div> : null}

            <Card>
                <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Account</th>
                                <th>Package</th>
                                <th className="num">Domains</th>
                                <th>Status</th>
                                <th>Created</th>
                                <th style={{ textAlign: 'right' }}>Actions</th>
                            </tr>
                        </thead>

                        {accounts === null ? (
                            <SkeletonRows rows={5} columns={6} />
                        ) : visible.length === 0 ? null : (
                            <tbody>
                                {visible.map((account) => (
                                    <tr key={account.id}>
                                        <td>
                                            <a {...linkProps(`/accounts/${account.username}`)} className="cell-main">
                                                <span className="strong mono">{account.username}</span>
                                                <span className="cell-sub">
                                                    {account.name} · {account.email}
                                                </span>
                                            </a>
                                        </td>
                                        <td>
                                            {account.package ? (
                                                <span>{account.package.name}</span>
                                            ) : (
                                                <span className="muted">none</span>
                                            )}
                                        </td>
                                        <td className="num">{account.domains}</td>
                                        <td>
                                            <Badge tone={statusTone(account.status)}>{account.status}</Badge>
                                        </td>
                                        <td className="muted">{when(account.createdAt)}</td>
                                        <td>
                                            <div className="row" style={{ justifyContent: 'flex-end', gap: 6 }}>
                                                {account.status === 'suspended' ? (
                                                    <Button
                                                        size="sm"
                                                        variant="ghost danger"
                                                        busy={busy === account.username}
                                                        onClick={() => void act(account, 'unsuspend')}
                                                    >
                                                        Unsuspend
                                                    </Button>
                                                ) : (
                                                    <Button
                                                        size="sm"
                                                        variant="ghost danger"
                                                        busy={busy === account.username}
                                                        onClick={() => void act(account, 'suspend')}
                                                    >
                                                        Suspend
                                                    </Button>
                                                )}
                                                <a className="btn sm" {...linkProps(`/accounts/${account.username}`)}>
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

                {accounts !== null && visible.length === 0 ? (
                    <Empty title={search ? 'No account matches that search' : 'No hosting accounts yet'}>
                        {search
                            ? 'Try part of a username, a name or an email address.'
                            : 'A hosting account is what owns a Linux user, its websites and its mail.'}
                    </Empty>
                ) : null}
            </Card>

            {creating ? (
                <CreateAccount
                    isAdmin={user.role === 'admin'}
                    onClose={() => setCreating(false)}
                    onCreated={(username) => {
                        setCreating(false);
                        onToast(`${username} is being created`);
                        void load();
                    }}
                />
            ) : null}
        </div>
    );
}

interface FormState {
    username: string;
    name: string;
    email: string;
    password: string;
    packageId: string;
    domain: string;
}

const blankForm: FormState = {
    username: '',
    name: '',
    email: '',
    password: '',
    packageId: '',
    domain: '',
};

function CreateAccount({
    isAdmin,
    onClose,
    onCreated,
}: {
    isAdmin: boolean;
    onClose: () => void;
    onCreated: (username: string) => void;
}) {
    const [form, setForm] = useState<FormState>(blankForm);
    const [packages, setPackages] = useState<Package[]>([]);
    const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
    const [error, setError] = useState('');
    const [busy, setBusy] = useState(false);

    useEffect(() => {
        api<{ packages: Package[] }>('/packages')
            .then((response) => {
                setPackages(response.packages);

                if (response.packages.length === 1 && response.packages[0]) {
                    setForm((current) => ({ ...current, packageId: String(response.packages[0]?.id) }));
                }
            })
            .catch(() => setPackages([]));
    }, []);

    function update(field: keyof FormState, value: string): void {
        setForm((current) => ({ ...current, [field]: value }));
        setFieldErrors((current) => ({ ...current, [field]: '' }));
    }

    async function submit(event: FormEvent): Promise<void> {
        event.preventDefault();
        setError('');
        setFieldErrors({});
        setBusy(true);

        try {
            const response = await api<{ account: Account }>('/accounts', {
                method: 'POST',
                body: {
                    username: form.username,
                    name: form.name,
                    email: form.email,
                    password: form.password,
                    packageId: form.packageId ? Number(form.packageId) : null,
                    domain: form.domain,
                },
            });

            onCreated(response.account.username);
        } catch (thrown) {
            if (thrown instanceof ApiError) {
                setFieldErrors(thrown.fields);
                setError(thrown.message);
            } else {
                setError(errorMessage(thrown));
            }

            setBusy(false);
        }
    }

    return (
        <Drawer
            title="New hosting account"
            subtitle="Creates a Linux user, its home directory and, if you give one, its first website."
            onClose={onClose}
            footer={
                <>
                    <Button variant="ghost" onClick={onClose}>
                        Cancel
                    </Button>
                    <Button variant="primary" type="submit" form="create-account" busy={busy}>
                        Create account
                    </Button>
                </>
            }
        >
            <form className="form-grid" onSubmit={submit} id="create-account">
                <Field
                    label="Username"
                    value={form.username}
                    onChange={(event) => update('username', event.target.value)}
                    error={fieldErrors['username']}
                    hint="3–16 lowercase letters or digits, starting with a letter."
                    autoCapitalize="none"
                    spellCheck={false}
                    required
                />
                <Field
                    label="Full name"
                    value={form.name}
                    onChange={(event) => update('name', event.target.value)}
                    error={fieldErrors['name']}
                    required
                />
                <Field
                    label="Email"
                    type="email"
                    value={form.email}
                    onChange={(event) => update('email', event.target.value)}
                    error={fieldErrors['email']}
                    required
                />
                <Field
                    label="Password"
                    type="text"
                    value={form.password}
                    onChange={(event) => update('password', event.target.value)}
                    error={fieldErrors['password']}
                    hint="At least 8 characters. Sent to the account owner by you, not by the panel."
                    autoComplete="new-password"
                    required
                />

                {packages.length > 0 ? (
                    <div className="field">
                        <label className="field-label" htmlFor="package">
                            Package
                        </label>
                        <select
                            id="package"
                            className="input"
                            value={form.packageId}
                            onChange={(event) => update('packageId', event.target.value)}
                        >
                            <option value="">No package (unlimited)</option>
                            {packages.map((pkg) => (
                                <option key={pkg.id} value={pkg.id}>
                                    {pkg.name}
                                </option>
                            ))}
                        </select>
                        {fieldErrors['packageId'] ? <p className="field-error">{fieldErrors['packageId']}</p> : null}
                    </div>
                ) : null}

                <Field
                    label="Primary domain"
                    value={form.domain}
                    onChange={(event) => update('domain', event.target.value)}
                    error={fieldErrors['domain']}
                    hint="Optional. Creates the website and its document root."
                    placeholder="example.com"
                    autoCapitalize="none"
                    spellCheck={false}
                />
            </form>

            {error && Object.keys(fieldErrors).length === 0 ? <div className="banner bad">{error}</div> : null}

            {!isAdmin ? (
                <div className="banner warn">
                    You are creating this account as a reseller. It counts against your own package.
                </div>
            ) : null}

            <p className="field-hint">
                The account starts empty and becomes active as soon as the server agent finishes creating the
                user. Nothing is written to disk until then.
            </p>
        </Drawer>
    );
}
