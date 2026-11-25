import { useCallback, useEffect, useState } from 'react';

import { api, megabytes, type Account, type RedisInstance } from '../api';
import { Badge, Button, Card, CardHead, Empty, errorMessage, SkeletonRows } from '../ui';

/**
 * Redis is one instance per hosting account, not per database. The panel keeps
 * the password and hands it back only when it is made or rotated.
 */
export function Redis({ onToast }: { onToast: (message: string, tone?: 'ok' | 'bad') => void }) {
    const [instance, setInstance] = useState<RedisInstance | null>(null);
    const [choices, setChoices] = useState<number[]>([64, 128, 256, 512, 1024]);
    const [accounts, setAccounts] = useState<Account[]>([]);
    const [loaded, setLoaded] = useState(false);
    const [error, setError] = useState('');
    const [username, setUsername] = useState('');
    const [memory, setMemory] = useState(256);
    const [busy, setBusy] = useState('');
    const [shownPassword, setShownPassword] = useState('');

    const load = useCallback(async () => {
        try {
            const answer = await api<{ instance: RedisInstance | null; choices: number[] }>('/redis');
            setInstance(answer.instance);
            setChoices(answer.choices);
            setError('');
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setLoaded(true);
        }
    }, []);

    useEffect(() => {
        void load();
        void api<{ accounts: Account[] }>('/accounts')
            .then((answer) => {
                const list = answer.accounts ?? [];
                setAccounts(list);
                setUsername((current) => current || list[0]?.username || '');
            })
            .catch(() => setAccounts([]));
    }, [load]);

    async function enable(): Promise<void> {
        setBusy('enable');
        setError('');

        try {
            const answer = await api<{ instance: RedisInstance; password: string }>('/redis', {
                method: 'POST',
                body: { username, maxMemoryMb: memory },
            });

            setInstance(answer.instance);
            setShownPassword(answer.password);
            onToast('Redis is being provisioned');
        } catch (thrown) {
            setError(errorMessage(thrown));
        } finally {
            setBusy('');
        }
    }

    async function rotate(): Promise<void> {
        if (!instance || !window.confirm('Replace the password? Anything using the old one stops connecting.')) {
            return;
        }

        setBusy('rotate');

        try {
            const answer = await api<{ instance: RedisInstance; password: string }>(`/redis/${instance.id}`, {
                method: 'POST',
            });

            setInstance(answer.instance);
            setShownPassword(answer.password);
            onToast('The password was replaced');
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy('');
        }
    }

    async function resize(value: number): Promise<void> {
        if (!instance) {
            return;
        }

        setBusy('resize');

        try {
            const answer = await api<{ instance: RedisInstance }>(`/redis/${instance.id}`, {
                method: 'PATCH',
                body: { maxMemoryMb: value },
            });

            setInstance(answer.instance);
            onToast(`Ceiling set to ${value} MB`);
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy('');
        }
    }

    async function remove(): Promise<void> {
        if (!instance || !window.confirm('Remove the cache? Its keys are deleted with the instance.')) {
            return;
        }

        setBusy('remove');

        try {
            await api(`/redis/${instance.id}`, { method: 'DELETE' });
            setInstance(null);
            setShownPassword('');
            onToast('Redis is being removed');
        } catch (thrown) {
            onToast(errorMessage(thrown), 'bad');
        } finally {
            setBusy('');
        }
    }

    return (
        <div className="page">
            <header className="page-head">
                <div>
                    <h1 className="page-title">Redis</h1>
                    <p className="page-sub">One cache instance per account, reachable from its applications</p>
                </div>
            </header>

            {error ? <div className="banner bad">{error}</div> : null}

            {shownPassword ? (
                <div className="banner ok">
                    Password for this instance, shown once: <code>{shownPassword}</code>
                </div>
            ) : null}

            {!loaded ? (
                <Card>
                    <SkeletonRows rows={2} />
                </Card>
            ) : instance ? (
                <>
                    <Card>
                        <CardHead title="Instance">
                            <Badge tone="ok">running</Badge>
                            <div className="row" style={{ marginLeft: 'auto' }}>
                                <Button busy={busy === 'rotate'} onClick={() => void rotate()}>
                                    New password
                                </Button>
                                <Button busy={busy === 'remove'} onClick={() => void remove()}>
                                    Remove
                                </Button>
                            </div>
                        </CardHead>

                        <dl className="kv">
                            <div>
                                <dt>Host</dt>
                                <dd className="mono">{instance.host}</dd>
                            </div>
                            <div>
                                <dt>Port</dt>
                                <dd className="mono">{instance.port}</dd>
                            </div>
                            <div>
                                <dt>Account</dt>
                                <dd>{instance.username}</dd>
                            </div>
                            <div>
                                <dt>Memory ceiling</dt>
                                <dd>{megabytes(instance.maxMemoryMb * 1024 * 1024)}</dd>
                            </div>
                        </dl>
                    </Card>

                    <Card>
                        <CardHead title="Memory ceiling">
                            <span className="muted">Redis evicts keys above this limit</span>
                        </CardHead>

                        <div className="row">
                            {choices.map((choice) => (
                                <Button
                                    key={choice}
                                    variant={choice === instance.maxMemoryMb ? 'primary' : undefined}
                                    busy={busy === 'resize'}
                                    onClick={() => void resize(choice)}
                                >
                                    {choice} MB
                                </Button>
                            ))}
                        </div>
                    </Card>
                </>
            ) : (
                <>
                    <Card>
                        <CardHead title="No cache yet" />
                        <Empty title="This account has no Redis instance">
                            <span className="muted">
                                An instance is a small Redis server with its own password, for caches and queues.
                            </span>
                        </Empty>
                    </Card>

                    <Card>
                        <CardHead title="Add a cache" />
                        <form
                            className="form"
                            style={{ maxWidth: 420 }}
                            onSubmit={(event) => {
                                event.preventDefault();
                                void enable();
                            }}
                        >
                            {accounts.length > 0 ? (
                                <label className="field">
                                    <span className="field-label">Account</span>
                                    <select
                                        className="input"
                                        value={username}
                                        onChange={(event) => setUsername(event.target.value)}
                                    >
                                        {accounts.map((account) => (
                                            <option key={account.id} value={account.username}>
                                                {account.username}
                                            </option>
                                        ))}
                                    </select>
                                </label>
                            ) : null}

                            <label className="field">
                                <span className="field-label">Memory ceiling</span>
                                <select
                                    className="input"
                                    value={String(memory)}
                                    onChange={(event) => setMemory(Number(event.target.value) || 256)}
                                >
                                    {choices.map((choice) => (
                                        <option key={choice} value={choice}>
                                            {choice} MB
                                        </option>
                                    ))}
                                </select>
                                <span className="field-hint">Redis evicts keys above this ceiling.</span>
                            </label>

                            <div className="row">
                                <Button variant="primary" type="submit" busy={busy === 'enable'}>
                                    Create cache
                                </Button>
                            </div>
                        </form>
                    </Card>
                </>
            )}
        </div>
    );
}
