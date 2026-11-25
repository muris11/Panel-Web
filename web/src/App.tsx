import { useEffect } from 'react';
import type { User } from './api';
import { navigate, usePath } from './router';
import { SessionProvider, useSessionProbe } from './session';
import { errorMessage, useToasts } from './ui';
import { AccountDetail } from './pages/AccountDetail';
import { Accounts } from './pages/Accounts';
import { Activity } from './pages/Activity';
import { Backups } from './pages/Backups';
import { Cron } from './pages/Cron';
import { Dashboard } from './pages/Dashboard';
import { Databases } from './pages/Databases';
import { DomainDetail } from './pages/DomainDetail';
import { Domains } from './pages/Domains';
import { Files, FilesPicker } from './pages/Files';
import { FTP } from './pages/FTP';
import { Login } from './pages/Login';
import { Mail } from './pages/Mail';
import { Postgres } from './pages/Postgres';
import { Redis } from './pages/Redis';
import { Server } from './pages/Server';
import { Settings } from './pages/Settings';
import { Shell } from './pages/Shell';

export default function App() {
    const { loading, user, agent, agentError, setUser } = useSessionProbe();
    const path = usePath();
    const toasts = useToasts();

    // A 401 from any screen means the session is gone; the probe's answer is
    // no longer true, so drop it and let the sign-in form come back.
    useEffect(() => {
        const onSignedOut = (): void => setUser(null);

        window.addEventListener('muriscore:signed-out', onSignedOut);

        return () => window.removeEventListener('muriscore:signed-out', onSignedOut);
    }, [setUser]);

    if (loading) {
        return <Boot />;
    }

    if (!user) {
        return (
            <Login
                agent={agent}
                onSignedIn={(signedIn: User) => {
                    setUser(signedIn);

                    // A reseller has no reason to land on an admin overview
                    // they cannot act on, and a deep link into an account
                    // should survive signing in.
                    if (!path.startsWith('/accounts')) {
                        navigate('/', { replace: true });
                    }
                }}
            />
        );
    }

    const onToast = (message: string, tone: 'ok' | 'bad' = 'ok'): void => toasts.push(message, tone);

    return (
        <SessionProvider user={user} agent={agent} agentError={agentError} onSignedOut={() => setUser(null)}>
            <Shell>
                <Screen path={path} onToast={onToast} />
            </Shell>
            {toasts.view}
        </SessionProvider>
    );
}

function Screen({ path, onToast }: { path: string; onToast: (message: string, tone?: 'ok' | 'bad') => void }) {
    if (path === '/' || path === '') {
        return <Dashboard />;
    }

    if (path === '/accounts') {
        return <Accounts onToast={onToast} />;
    }

    const accountMatch = /^\/accounts\/([^/]+)\/?$/.exec(path);

    if (accountMatch?.[1]) {
        return <AccountDetail username={decodeURIComponent(accountMatch[1])} onToast={onToast} />;
    }

    if (path === '/domains') {
        return <Domains />;
    }

    const domainMatch = /^\/domains\/(\d+)\/?$/.exec(path);

    if (domainMatch?.[1]) {
        return <DomainDetail id={Number(domainMatch[1])} onToast={onToast} />;
    }

    if (path === '/mail') {
        return <Mail onToast={onToast} />;
    }

    if (path === '/databases') {
        return <Databases onToast={onToast} />;
    }

    if (path === '/postgres') {
        return <Postgres onToast={onToast} />;
    }

    if (path === '/redis') {
        return <Redis onToast={onToast} />;
    }

    if (path === '/ftp') {
        return <FTP onToast={onToast} />;
    }

    if (path === '/files' || path === '/files/') {
        return <FilesPicker />;
    }

    const filesMatch = /^\/files\/([^/]+)(?:\/(.*))?$/.exec(path);

    if (filesMatch?.[1]) {
        return (
            <Files
                username={decodeURIComponent(filesMatch[1])}
                path={(filesMatch[2] ?? '').replace(/\/$/, '')}
            />
        );
    }

    const cronMatch = /^\/accounts\/([^/]+)\/cron\/?$/.exec(path);

    if (cronMatch?.[1]) {
        return <Cron username={decodeURIComponent(cronMatch[1])} onToast={onToast} />;
    }

    const backupMatch = /^\/accounts\/([^/]+)\/backups\/?$/.exec(path);

    if (backupMatch?.[1]) {
        return <Backups username={decodeURIComponent(backupMatch[1])} onToast={onToast} />;
    }

    const accountFilesMatch = /^\/accounts\/([^/]+)\/files(?:\/(.*))?$/.exec(path);

    if (accountFilesMatch?.[1]) {
        return (
            <Files
                username={decodeURIComponent(accountFilesMatch[1])}
                path={(accountFilesMatch[2] ?? '').replace(/\/$/, '')}
            />
        );
    }

    if (path === '/server') {
        return <Server onToast={onToast} />;
    }

    if (path === '/activity') {
        return <Activity />;
    }

    if (path === '/settings') {
        return <Settings onToast={onToast} />;
    }

    return <NotFound />;
}

function NotFound() {
    return (
        <div className="page">
            <header className="page-head">
                <div>
                    <h1 className="page-title">Page not found</h1>
                    <p className="page-sub">That address does not exist in the panel.</p>
                </div>
            </header>
            <a className="btn" href="/">
                Back to the overview
            </a>
        </div>
    );
}

/** Placeholder while the first probe is in flight. It is the shape of the
 *  panel, not a spinner in the middle of an empty page. */
function Boot() {
    return (
        <div className="app" aria-busy="true">
            <aside className="sidebar">
                <div className="brand">
                    <div className="brand-mark">R</div>
                    <div>
                        <div className="brand-name">MurisCore</div>
                        <span className="brand-sub">Control panel</span>
                    </div>
                </div>
            </aside>
            <main className="main">
                <div className="page">
                    <div className="skeleton" style={{ height: 26, width: 180 }} />
                    <div className="grid-4">
                        {[0, 1, 2, 3].map((key) => (
                            <div key={key} className="card">
                                <div className="stat">
                                    <span className="skeleton" style={{ height: 11, width: 90 }} />
                                    <span className="skeleton" style={{ height: 26, width: 64 }} />
                                </div>
                            </div>
                        ))}
                    </div>
                    <div className="card">
                        <div className="stat">
                            <span className="skeleton" style={{ height: 120 }} />
                        </div>
                    </div>
                </div>
            </main>
        </div>
    );
}

/** Kept so an unexpected render error still says something an operator can
 *  report, instead of a blank page. */
export function errorText(error: unknown): string {
    return errorMessage(error);
}
