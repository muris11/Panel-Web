import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';

import { api, when, type SessionRow, type TokenRow } from '../api';
import { useSession } from '../session';
import { Badge, Button, Card, CardHead, Drawer, Empty, errorMessage, Field, SkeletonRows } from '../ui';

/** Account and session settings. Deliberately short: everything here is
 *  something an operator can act on. */
export function Settings({ onToast }: { onToast: (message: string, tone?: 'ok' | 'bad') => void }) {
    const { user, agent, signOut, updateUser } = useSession();

    const [sessions, setSessions] = useState<SessionRow[] | null>(null);
    const [error, setError] = useState('');
    const [busy, setBusy] = useState<string | null>(null);

    const load = useCallback(async () => {
        try {
            const response = await api<{ sessions: SessionRow[] }>('/sessions');
            setSessions(response.sessions);
            setError('');
        } catch (thrown) {
            setError(errorMessage(thrown));
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    async function revoke(session: SessionRow): Promise<void> {
        setBusy(session.id);

        try {
            await api(`/sessions/${encodeURIComponent(session.id)}`, { method: 'DELETE' });
            onToast('That session was signed out');
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
                    <h1 className="page-title">Settings</h1>
                    <p className="page-sub">Your account and where you are signed in</p>
                </div>
            </header>

            {error ? <div className="banner bad">{error}</div> : null}

            <div className="grid-2">
                <Card>
                    <CardHead title="Your account" />
                    <div className="card-body">
                        <dl className="kv">
                            <dt>Username</dt>
                            <dd className="mono">{user.username}</dd>
                            <dt>Name</dt>
                            <dd>{user.name}</dd>
                            <dt>Email</dt>
                            <dd>{user.email}</dd>
                            <dt>Role</dt>
                            <dd style={{ textTransform: 'capitalize' }}>{user.role}</dd>
                            <dt>Two-factor</dt>
                            <dd>
                                {user.twoFactor ? (
                                    <Badge tone="ok">enabled</Badge>
                                ) : (
                                    <Badge tone="warn">not set up</Badge>
                                )}
                            </dd>
                            <dt>Last sign in</dt>
                            <dd>{when(user.lastLoginAt)}</dd>
                        </dl>
                    </div>
                </Card>

                <Card>
                    <CardHead title="Server agent" />
                    <div className="card-body">
                        <dl className="kv">
                            <dt>Driver</dt>
                            <dd className="mono">{agent}</dd>
                            <dt>Socket</dt>
                            <dd className="mono muted">/run/muriscore/agent.sock</dd>
                            <dt>Privileges</dt>
                            <dd className="muted">
                                muriscored runs as root; this panel never does. Every change is a named call to it.
                            </dd>
                        </dl>

                        {agent !== 'socket' ? (
                            <div className="banner warn" style={{ marginTop: 14 }}>
                                The agent is simulated. Signing in, listing and editing records works; nothing is
                                written to a real server.
                            </div>
                        ) : null}
                    </div>
                </Card>
            </div>

            <Profile onSaved={(next) => updateUser(next)} onToast={onToast} />

            <TwoFactor />

            <Card>
                <CardHead title="Signed-in sessions">
                    <Button size="sm" variant="ghost" onClick={() => void load()}>
                        Refresh
                    </Button>
                </CardHead>
                <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Address</th>
                                <th>Browser</th>
                                <th>Started</th>
                                <th>Last seen</th>
                                <th style={{ textAlign: 'right' }}>Action</th>
                            </tr>
                        </thead>
                        {sessions === null ? (
                            <SkeletonRows rows={2} columns={5} />
                        ) : (
                            <tbody>
                                {sessions.map((session) => (
                                    <tr key={session.id}>
                                        <td className="mono">
                                            {session.ip}
                                            {session.current ? (
                                                <span style={{ marginLeft: 8 }}>
                                                    <Badge tone="ok">this one</Badge>
                                                </span>
                                            ) : null}
                                        </td>
                                        <td className="muted" style={{ maxWidth: 280 }}>
                                            <span title={session.userAgent}>{shortAgent(session.userAgent)}</span>
                                        </td>
                                        <td className="muted">{when(session.createdAt)}</td>
                                        <td className="muted">{when(session.lastSeenAt)}</td>
                                        <td style={{ textAlign: 'right' }}>
                                            {session.current ? (
                                                <Button size="sm" variant="ghost" onClick={() => void signOut()}>
                                                    Sign out
                                                </Button>
                                            ) : (
                                                <Button
                                                    size="sm"
                                                    variant="danger"
                                                    busy={busy === session.id}
                                                    onClick={() => void revoke(session)}
                                                >
                                                    End
                                                </Button>
                                            )}
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        )}
                    </table>
                </div>
                {sessions !== null && sessions.length === 0 ? (
                    <Empty title="No sessions recorded">Sign in from another browser to see it listed here.</Empty>
                ) : null}
            </Card>

            <Tokens onToast={onToast} />
        </div>
    );
}

/** Profile: the name and address this account answers to, and the password it
 *  signs in with. Changing the password signs every other browser out. */
function Profile({
    onSaved,
    onToast,
}: {
    onSaved: (patch: { name: string; email: string }) => void;
    onToast: (message: string, tone?: 'ok' | 'bad') => void;
}) {
    const { user } = useSession();

    const [name, setName] = useState(user.name);
    const [email, setEmail] = useState(user.email);
    const [current, setCurrent] = useState('');
    const [next, setNext] = useState('');
    const [repeat, setRepeat] = useState('');
    const [details, setDetails] = useState<Record<string, string>>({});
    const [passwordFields, setPasswordFields] = useState<Record<string, string>>({});
    const [savingDetails, setSavingDetails] = useState(false);
    const [savingPassword, setSavingPassword] = useState(false);

    async function saveDetails(event: FormEvent): Promise<void> {
        event.preventDefault();
        setSavingDetails(true);
        setDetails({});

        try {
            const answer = await api<{ user: { name: string; email: string } }>('/me', {
                method: 'PATCH',
                body: { name, email },
            });

            onSaved({ name: answer.user.name, email: answer.user.email });
            onToast('Your details were saved');
        } catch (thrown) {
            setDetails((thrown as { fields?: Record<string, string> }).fields ?? {});
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setSavingDetails(false);
        }
    }

    async function savePassword(event: FormEvent): Promise<void> {
        event.preventDefault();
        setPasswordFields({});

        if (next !== repeat) {
            setPasswordFields({ repeat: 'The two passwords are not the same.' });

            return;
        }

        setSavingPassword(true);

        try {
            await api('/me/password', { method: 'POST', body: { currentPassword: current, newPassword: next } });
            setCurrent('');
            setNext('');
            setRepeat('');
            onToast('Password changed; other browsers were signed out');
        } catch (thrown) {
            setPasswordFields((thrown as { fields?: Record<string, string> }).fields ?? {});
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setSavingPassword(false);
        }
    }

    return (
        <div className="grid-2">
            <Card>
                <CardHead title="Your details" />
                <form className="form" onSubmit={(event) => void saveDetails(event)}>
                    <Field
                        label="Name"
                        value={name}
                        onChange={(event) => setName(event.target.value)}
                        error={details.name}
                        required
                    />
                    <Field
                        label="Email address"
                        type="email"
                        value={email}
                        onChange={(event) => setEmail(event.target.value)}
                        hint="Used to sign in as well as the username."
                        error={details.email}
                        required
                    />
                    <div className="row">
                        <Button variant="primary" type="submit" busy={savingDetails}>
                            Save details
                        </Button>
                    </div>
                </form>
            </Card>

            <Card>
                <CardHead title="Password" />
                <form className="form" onSubmit={(event) => void savePassword(event)}>
                    <Field
                        label="Current password"
                        type="password"
                        value={current}
                        onChange={(event) => setCurrent(event.target.value)}
                        error={passwordFields.currentPassword}
                        autoComplete="current-password"
                        required
                    />
                    <Field
                        label="New password"
                        type="password"
                        value={next}
                        onChange={(event) => setNext(event.target.value)}
                        hint="At least 12 characters."
                        error={passwordFields.newPassword}
                        autoComplete="new-password"
                        required
                    />
                    <Field
                        label="Repeat the new password"
                        type="password"
                        value={repeat}
                        onChange={(event) => setRepeat(event.target.value)}
                        error={passwordFields.repeat}
                        autoComplete="new-password"
                        required
                    />
                    <div className="row">
                        <Button variant="primary" type="submit" busy={savingPassword}>
                            Change password
                        </Button>
                    </div>
                </form>
            </Card>
        </div>
    );
}

/** Two-factor authentication: enrol, confirm, turn off. The secret is written
 *  unconfirmed first, so a closed tab cannot lock the account out. */
function TwoFactor() {
    const { user } = useSession();
    const [enabled, setEnabled] = useState(user.twoFactor);
    const [step, setStep] = useState<'idle' | 'enrol' | 'off'>('idle');
    const [secret, setSecret] = useState('');
    const [url, setUrl] = useState('');
    const [codes, setCodes] = useState<string[]>([]);
    const [code, setCode] = useState('');
    const [credential, setCredential] = useState('');
    const [error, setError] = useState('');
    const [busy, setBusy] = useState('');

    async function start(): Promise<void> {
        setBusy('start');
        setError('');

        try {
            const answer = await api<{ secret: string; otpauthUrl: string; recoveryCodes: string[] }>(
                '/me/two-factor',
                { method: 'POST', body: {} },
            );

            setSecret(answer.secret);
            setUrl(answer.otpauthUrl);
            setCodes(answer.recoveryCodes);
            setStep('enrol');
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy('');
        }
    }

    async function confirm(): Promise<void> {
        setBusy('confirm');
        setError('');

        try {
            await api('/me/two-factor/confirm', { method: 'POST', body: { code } });
            setEnabled(true);
            setStep('idle');
            setCode('');
            setCodes([]);
            setSecret('');
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy('');
        }
    }

    async function turnOff(): Promise<void> {
        setBusy('off');
        setError('');

        try {
            await api('/me/two-factor', { method: 'DELETE', body: { password: credential } });
            setEnabled(false);
            setStep('idle');
            setCredential('');
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy('');
        }
    }

    return (
        <Card>
            <CardHead title="Two-factor authentication">
                <Badge tone={enabled ? 'ok' : 'warn'}>{enabled ? 'enabled' : 'not set up'}</Badge>
            </CardHead>

            <div className="card-body">
                <p className="muted" style={{ margin: 0 }}>
                    A six-digit code from an authenticator app is asked for after the password. Recovery codes are
                    handed out once, at setup.
                </p>

                <div className="row" style={{ justifyContent: 'flex-end', marginTop: 12 }}>
                    {enabled ? (
                        <Button variant="danger" onClick={() => setStep('off')}>
                            Turn off
                        </Button>
                    ) : (
                        <Button variant="primary" busy={busy === 'start'} onClick={() => void start()}>
                            Set up
                        </Button>
                    )}
                </div>
            </div>

            {step === 'enrol' ? (
                <Drawer
                    title="Set up two-factor"
                    subtitle="Add the secret to your authenticator app, then type the code it shows."
                    onClose={() => setStep('idle')}
                    footer={
                        <>
                            <Button onClick={() => setStep('idle')}>Cancel</Button>
                            <Button variant="primary" busy={busy === 'confirm'} onClick={() => void confirm()}>
                                Confirm code
                            </Button>
                        </>
                    }
                >
                    {error ? <div className="banner bad">{error}</div> : null}

                    <div className="field">
                        <span className="field-label">Secret</span>
                        <code className="mono" style={{ wordBreak: 'break-all' }}>
                            {secret}
                        </code>
                    </div>

                    <div className="field">
                        <span className="field-label">Setup link</span>
                        <code className="mono" style={{ wordBreak: 'break-all', fontSize: 11 }}>
                            {url}
                        </code>
                        <span className="field-hint">Paste it into the app if the secret is awkward to type.</span>
                    </div>

                    <div className="field">
                        <span className="field-label">Recovery codes</span>
                        <div className="grid-2 tight mono" style={{ fontSize: 12 }}>
                            {codes.map((recovery) => (
                                <span key={recovery}>{recovery}</span>
                            ))}
                        </div>
                        <span className="field-hint">Each one signs you in once. Store them somewhere safe.</span>
                    </div>

                    <Field
                        label="Code from the app"
                        value={code}
                        onChange={(event) => setCode(event.target.value)}
                        placeholder="123456"
                        inputMode="numeric"
                        autoComplete="one-time-code"
                        required
                    />
                </Drawer>
            ) : null}

            {step === 'off' ? (
                <Drawer
                    title="Turn off two-factor"
                    subtitle="Your password is needed, so a borrowed browser cannot remove it."
                    onClose={() => setStep('idle')}
                    footer={
                        <>
                            <Button onClick={() => setStep('idle')}>Cancel</Button>
                            <Button variant="danger" busy={busy === 'off'} onClick={() => void turnOff()}>
                                Turn off
                            </Button>
                        </>
                    }
                >
                    {error ? <div className="banner bad">{error}</div> : null}

                    <Field
                        label="Password"
                        type="password"
                        value={credential}
                        onChange={(event) => setCredential(event.target.value)}
                        autoComplete="current-password"
                        required
                    />
                </Drawer>
            ) : null}
        </Card>
    );
}

/** API tokens sign a request with a Bearer header instead of a cookie, which is
 *  what scripts and CI use. The plaintext is shown once; only a hash is kept. */
function Tokens({ onToast }: { onToast: (message: string, tone?: 'ok' | 'bad') => void }) {
    const [tokens, setTokens] = useState<TokenRow[] | null>(null);
    const [name, setName] = useState('');
    const [plain, setPlain] = useState('');
    const [error, setError] = useState('');
    const [busy, setBusy] = useState('');

    const load = useCallback(async () => {
        try {
            const response = await api<{ tokens: TokenRow[] }>('/tokens');
            setTokens(response.tokens);
        } catch (thrown) {
            setError(errorMessage(thrown));
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    async function create(): Promise<void> {
        setBusy('create');
        setError('');

        try {
            const answer = await api<{ token: TokenRow; secret: string }>('/tokens', {
                method: 'POST',
                body: { name },
            });

            setName('');
            setPlain(answer.secret);
            onToast('The token was created');
            await load();
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy('');
        }
    }

    async function remove(token: TokenRow): Promise<void> {
        if (!window.confirm(`Revoke ${token.name}? Anything using it stops working.`)) {
            return;
        }

        setBusy(String(token.id));

        try {
            await api(`/tokens/${token.id}`, { method: 'DELETE' });
            onToast('The token was revoked');
            await load();
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy('');
        }
    }

    return (
        <Card>
            <CardHead title="API tokens">
                <Badge tone="plain">{tokens?.length ?? 0}</Badge>
            </CardHead>

            {error ? <div className="banner bad">{error}</div> : null}

            {plain ? (
                <div className="banner" style={{ margin: '0 16px 12px' }}>
                    <div className="strong">Copy this now: it is not shown again.</div>
                    <code className="mono" style={{ display: 'block', marginTop: 6, wordBreak: 'break-all' }}>
                        {plain}
                    </code>
                </div>
            ) : null}

            <div className="table-wrap">
                <table>
                    <thead>
                        <tr>
                            <th>Name</th>
                            <th>Created</th>
                            <th>Last used</th>
                            <th>Expires</th>
                            <th style={{ textAlign: 'right' }}>Action</th>
                        </tr>
                    </thead>
                    {tokens === null ? (
                        <SkeletonRows rows={2} columns={5} />
                    ) : (
                        <tbody>
                            {tokens.map((token) => (
                                <tr key={token.id}>
                                    <td className="strong">{token.name}</td>
                                    <td className="muted">{when(token.createdAt)}</td>
                                    <td className="muted">{token.lastUsedAt ? when(token.lastUsedAt) : 'never'}</td>
                                    <td className="muted">{token.expiresAt ? when(token.expiresAt) : 'never'}</td>
                                    <td style={{ textAlign: 'right' }}>
                                        <Button
                                            size="sm"
                                            variant="danger"
                                            busy={busy === String(token.id)}
                                            onClick={() => void remove(token)}
                                        >
                                            Revoke
                                        </Button>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    )}
                </table>
            </div>

            {tokens !== null && tokens.length === 0 ? (
                <Empty title="No tokens">A token lets a script call this API without a browser session.</Empty>
            ) : null}

            <div className="card-body">
                <form
                    className="row"
                    onSubmit={(event) => {
                        event.preventDefault();
                        void create();
                    }}
                >
                    <Field
                        label="Token name"
                        value={name}
                        onChange={(event) => setName(event.target.value)}
                        placeholder="deploy script"
                        required
                    />
                    <Button type="submit" variant="primary" busy={busy === 'create'}>
                        Create token
                    </Button>
                </form>
            </div>
        </Card>
    );
}

/** One readable phrase out of a user agent string. */
function shortAgent(agent: string): string {
    if (!agent) {
        return 'unknown browser';
    }

    const browser = /Firefox\/[\d.]+/.exec(agent)?.[0] ?? /Edg\/[\d.]+/.exec(agent)?.[0] ?? /Chrome\/[\d.]+/.exec(agent)?.[0] ?? /Safari\/[\d.]+/.exec(agent)?.[0];

    const system = /Windows/.test(agent)
        ? 'Windows'
        : /Macintosh|Mac OS X/.test(agent)
          ? 'macOS'
          : /Android/.test(agent)
            ? 'Android'
            : /iPhone|iPad/.test(agent)
              ? 'iOS'
              : /Linux/.test(agent)
                ? 'Linux'
                : '';

    const parts = [browser?.split('/')[0], system].filter(Boolean);

    return parts.length > 0 ? parts.join(' on ') : 'unknown browser';
}
