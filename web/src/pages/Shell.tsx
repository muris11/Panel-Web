import type { ReactNode } from 'react';

import { linkProps, usePath } from '../router';
import { useSession } from '../session';
import {
    IconActivity,
    IconDatabase,
    IconFolder,
    IconGauge,
    IconGlobe,
    IconMail,
    IconServer,
    IconSettings,
    IconUsers,
} from '../ui';

interface NavEntry {
    to: string;
    label: string;
    icon: ReactNode;
}

/** The frame every screen sits in: brand, navigation, and who is signed in.
 *  It stays on screen during navigation, so only the content area repaints. */
export function Shell({ children }: { children: ReactNode }) {
    const { user, agent, signOut } = useSession();
    const path = usePath();

    const hosting: NavEntry[] = [
        { to: '/domains', label: 'Domains', icon: <IconGlobe /> },
        { to: '/mail', label: 'Mail', icon: <IconMail /> },
        { to: '/databases', label: 'Databases', icon: <IconDatabase /> },
        { to: '/ftp', label: 'FTP', icon: <IconFolder /> },
    ];

    const entries: NavEntry[] = [
        { to: '/', label: 'Overview', icon: <IconGauge /> },
        { to: '/accounts', label: 'Accounts', icon: <IconUsers /> },
        ...hosting,
        { to: '/files', label: 'Files', icon: <IconFolder /> },
        ...(user.role === 'admin' ? [{ to: '/server', label: 'Server', icon: <IconServer /> }] : []),
        { to: '/activity', label: 'Activity', icon: <IconActivity /> },
        { to: '/settings', label: 'Settings', icon: <IconSettings /> },
    ];

    const initials = (user.name || user.username).slice(0, 2);

    return (
        <div className="app">
            <aside className="sidebar">
                <div className="brand">
                    <div className="brand-mark">R</div>
                    <div>
                        <div className="brand-name">RAYXO</div>
                        <span className="brand-sub">Control panel</span>
                    </div>
                </div>

                <nav className="nav" aria-label="Main">
                    <span className="nav-label">Manage</span>
                    {entries.map((entry) => {
                        const active = entry.to === '/' ? path === '/' : path.startsWith(entry.to);

                        return (
                            <a
                                key={entry.to}
                                {...linkProps(entry.to)}
                                className={`nav-item ${active ? 'is-active' : ''}`.trim()}
                                aria-current={active ? 'page' : undefined}
                            >
                                {entry.icon}
                                {entry.label}
                            </a>
                        );
                    })}
                </nav>

                <div className="sidebar-foot">
                    <span className="agent-line">
                        <span className={`dot ${agent === 'socket' ? '' : 'sim'}`} />
                        {agent === 'socket' ? 'rayxod connected' : 'simulated agent'}
                    </span>

                    <div className="who">
                        <div className="avatar" aria-hidden="true">
                            {initials}
                        </div>
                        <div className="who-text">
                            <div className="who-name">{user.name || user.username}</div>
                            <div className="who-role">{user.role}</div>
                        </div>
                        <button className="btn-icon" onClick={() => void signOut()} title="Sign out" aria-label="Sign out">
                            <svg viewBox="0 0 24 24" className="nav-icon" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
                                <path d="M15 4h3.5A1.5 1.5 0 0 1 20 5.5v13a1.5 1.5 0 0 1-1.5 1.5H15" />
                                <path d="M10 8l-4 4 4 4M6 12h9" />
                            </svg>
                        </button>
                    </div>
                </div>
            </aside>

            <main className="main">{children}</main>
        </div>
    );
}
