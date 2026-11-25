import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';

import { api, ApiError, setCSRFToken, type User } from './api';

interface SessionValue {
    user: User;
    agent: string;
    agentError: string | null;
    signOut: () => Promise<void>;
    updateUser: (patch: Partial<User>) => void;
}

const SessionContext = createContext<SessionValue | null>(null);

export function useSession(): SessionValue {
    const value = useContext(SessionContext);

    if (!value) {
        throw new Error('useSession is only available inside the signed-in panel');
    }

    return value;
}

/** Probe once at startup. A panel that guesses it is signed in shows a flash
 *  of the dashboard before bouncing the operator back to the sign-in form. */
export function useSessionProbe(): {
    loading: boolean;
    user: User | null;
    agent: string;
    agentError: string | null;
    setUser: (user: User | null) => void;
} {
    const [loading, setLoading] = useState(true);
    const [user, setUser] = useState<User | null>(null);
    const [agent, setAgent] = useState('unknown');
    const [agentError, setAgentError] = useState<string | null>(null);

    useEffect(() => {
        let live = true;

        api<{ authenticated: boolean; user?: User; csrfToken?: string; agent?: string }>('/auth/session')
            .then((response) => {
                if (!live) {
                    return;
                }

                if (response.csrfToken) {
                    setCSRFToken(response.csrfToken);
                }

                if (response.agent) {
                    setAgent(response.agent);
                }

                setUser(response.authenticated && response.user ? response.user : null);
            })
            .catch((error: unknown) => {
                if (live && error instanceof ApiError && error.status === 503) {
                    setAgentError(error.message);
                }
            })
            .finally(() => {
                if (live) {
                    setLoading(false);
                }
            });

        return () => {
            live = false;
        };
    }, []);

    return { loading, user, agent, agentError, setUser };
}

export function SessionProvider({
    user,
    agent,
    agentError,
    onSignedOut,
    children,
}: {
    user: User;
    agent: string;
    agentError: string | null;
    onSignedOut: () => void;
    children: ReactNode;
}) {
    const [current, setCurrent] = useState(user);

    const signOut = useCallback(async () => {
        try {
            await api<{ signedOut: boolean }>('/auth/logout', { method: 'POST' });
        } finally {
            // Even if the call failed, the operator asked to leave: drop the
            // client state so the form comes back.
            onSignedOut();
        }
    }, [onSignedOut]);

    const value = useMemo<SessionValue>(
        () => ({
            user: current,
            agent,
            agentError,
            signOut,
            updateUser: (patch) => setCurrent((previous) => ({ ...previous, ...patch })),
        }),
        [current, agent, agentError, signOut],
    );

    return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}
