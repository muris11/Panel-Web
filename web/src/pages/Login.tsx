import { useState } from 'react';
import type { FormEvent } from 'react';

import { api, ApiError, setCSRFToken, type User } from '../api';
import { Button, errorMessage, Field } from '../ui';

/**
 * The sign-in form. One screen, no tabs: a code field appears only when the
 * account has a second factor, because asking everybody for a code they do
 * not have is a worse form than asking later for the few who do.
 */
export function Login({
    agent,
    onSignedIn,
}: {
    agent: string;
    onSignedIn: (user: User) => void;
}) {
    const [login, setLogin] = useState('');
    const [password, setPassword] = useState('');
    const [code, setCode] = useState('');
    const [needsCode, setNeedsCode] = useState(false);
    const [error, setError] = useState('');
    const [busy, setBusy] = useState(false);

    async function submit(event: FormEvent): Promise<void> {
        event.preventDefault();
        setError('');
        setBusy(true);

        try {
            const response = await api<{ authenticated: boolean; user: User; csrfToken: string }>(
                '/auth/login',
                {
                    method: 'POST',
                    body: { login, password, totp: code },
                },
            );

            setCSRFToken(response.csrfToken);
            onSignedIn(response.user);
        } catch (thrown) {
            if (thrown instanceof ApiError && thrown.code === 'two_factor_required') {
                setNeedsCode(true);
                setError('');
            } else {
                setError(errorMessage(thrown));
            }

            setBusy(false);
        }
    }

    return (
        <div className="auth">
            <div className="auth-card">
                <div className="auth-brand">
                    <div className="brand-mark">M</div>
                    <div>
                        <h1 className="auth-title">Sign in to MurisCore</h1>
                        <p className="auth-sub">Server control panel</p>
                    </div>
                </div>

                <form className="auth-form" onSubmit={submit}>
                    <Field
                        label="Username or email"
                        autoFocus
                        autoComplete="username"
                        autoCapitalize="none"
                        spellCheck={false}
                        value={login}
                        onChange={(event) => setLogin(event.target.value)}
                        required
                    />

                    <Field
                        label="Password"
                        type="password"
                        autoComplete="current-password"
                        value={password}
                        onChange={(event) => setPassword(event.target.value)}
                        required
                    />

                    {needsCode ? (
                        <Field
                            label="Authenticator code"
                            inputMode="numeric"
                            autoComplete="one-time-code"
                            maxLength={6}
                            placeholder="123456"
                            hint="Six digits from your authenticator app."
                            value={code}
                            onChange={(event) => setCode(event.target.value)}
                        />
                    ) : null}

                    {error ? (
                        <div className="banner bad" role="alert">
                            {error}
                        </div>
                    ) : null}

                    <Button variant="primary" type="submit" busy={busy}>
                        {busy ? 'Signing in' : 'Sign in'}
                    </Button>
                </form>

                <div className="auth-foot">
                    <span className="agent-line">
                        <span className={`dot ${agent === 'socket' ? '' : 'sim'}`} />
                        {agent === 'socket' ? 'Connected to muriscored' : 'Simulated agent — no server changes'}
                    </span>
                    <span>MurisCore panel · AGPL-3.0</span>
                </div>
            </div>
        </div>
    );
}
