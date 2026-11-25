import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';

import { api, when } from '../api';
import { Badge, Button, Card, CardHead, Drawer, Empty, errorMessage, SkeletonRows } from '../ui';

interface Announcement {
    id: number;
    from?: string;
    subject: string;
    body: string;
    audience?: string[];
    recipient_count?: number;
    emailed?: boolean;
    created_at: string;
}

interface AnnouncementPage {
    received: Announcement[] | null;
    sent: Announcement[] | null;
    can_send: boolean;
    audiences: Record<string, number>;
    max_body: number;
}

/** Messages an administrator or a reseller sends to many accounts at once. */
export function Announcements({
    role,
    onToast,
}: {
    role: 'admin' | 'reseller' | 'user';
    onToast: (message: string, tone?: 'ok' | 'bad') => void;
}) {
    const [page, setPage] = useState<AnnouncementPage | null>(null);
    const [error, setError] = useState('');
    const [composing, setComposing] = useState(false);
    const [subject, setSubject] = useState('');
    const [body, setBody] = useState('');
    const [audience, setAudience] = useState<string[]>(role === 'admin' ? ['user'] : []);
    const [email, setEmail] = useState(true);
    const [busy, setBusy] = useState(false);
    const [formError, setFormError] = useState('');

    const load = useCallback(async () => {
        setError('');

        try {
            setPage(await api<AnnouncementPage>('/announcements'));
        } catch (problem) {
            setError(errorMessage(problem));
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const toggle = (role: string): void => {
        setAudience((current) =>
            current.includes(role) ? current.filter((value) => value !== role) : [...current, role],
        );
    };

    const send = async (event: FormEvent): Promise<void> => {
        event.preventDefault();
        setBusy(true);
        setFormError('');

        try {
            const created = await api<Announcement>('/announcements', {
                method: 'POST',
                body: { subject, body, audience, email },
            });

            setComposing(false);
            setSubject('');
            setBody('');
            onToast(`Sent to ${created.recipient_count ?? 0} account(s).`);
            await load();
        } catch (problem) {
            setFormError(errorMessage(problem));
        } finally {
            setBusy(false);
        }
    };

    const received = page?.received ?? [];
    const sent = page?.sent ?? [];
    const audiences = page?.audiences ?? {};
    const total = audience.reduce((sum, key) => sum + (audiences[key] ?? 0), 0);

    return (
        <>
            <div className="page-head">
                <div>
                    <h1 className="page-title">Announcements</h1>
                    <p className="page-sub">
                        {page?.can_send
                            ? 'Messages from the administrator to you, and the ones you sent.'
                            : 'Messages the administrator sent to the accounts on this server.'}
                    </p>
                </div>
                {page?.can_send ? (
                    <Button variant="primary" onClick={() => setComposing(true)}>
                        Write a message
                    </Button>
                ) : null}
            </div>

            {error ? (
                <div className="banner bad" role="alert">
                    {error}
                </div>
            ) : null}

            <Card>
                <CardHead title="Received" />
                {page === null ? (
                    <SkeletonRows rows={3} columns={2} />
                ) : received.length === 0 ? (
                    <Empty title="Nothing yet">Messages addressed to your accounts appear here.</Empty>
                ) : (
                    received.map((announcement) => (
                        <article className="announcement" key={announcement.id}>
                            <h3>{announcement.subject}</h3>
                            <p className="muted">
                                {announcement.from ?? 'Server administrator'} · {when(announcement.created_at)}
                            </p>
                            <p>{announcement.body}</p>
                        </article>
                    ))
                )}
            </Card>

            {page?.can_send ? (
                <Card>
                    <CardHead title="Sent" />
                    {sent.length === 0 ? (
                        <Empty title="Nothing sent yet">Each message you send is listed here with its reach.</Empty>
                    ) : (
                        <div className="table-wrap">
                            <table>
                                <thead>
                                    <tr>
                                        <th>Subject</th>
                                        <th>Audience</th>
                                        <th className="num">Accounts</th>
                                        <th>Email</th>
                                        <th>Sent</th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {sent.map((announcement) => (
                                        <tr key={announcement.id}>
                                            <td>{announcement.subject}</td>
                                            <td>{(announcement.audience ?? []).join(', ') || '—'}</td>
                                            <td className="num">{announcement.recipient_count ?? 0}</td>
                                            <td>
                                                <Badge tone={announcement.emailed ? 'ok' : 'plain'}>
                                                    {announcement.emailed ? 'queued' : 'panel only'}
                                                </Badge>
                                            </td>
                                            <td>{when(announcement.created_at)}</td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        </div>
                    )}
                </Card>
            ) : null}

            {composing ? (
                <Drawer
                    title="Write a message"
                    subtitle="It appears in every chosen account's Announcements page."
                    onClose={() => setComposing(false)}
                    footer={
                        <>
                            <Button onClick={() => setComposing(false)}>Cancel</Button>
                            <Button variant="primary" onClick={(event) => void send(event)} busy={busy}>
                                Send to {total} account{total === 1 ? '' : 's'}
                            </Button>
                        </>
                    }
                >
                    <form className="form" onSubmit={(event) => void send(event)}>
                        <label className="field-label" htmlFor="announcement-subject">
                            Subject
                        </label>
                        <input
                            id="announcement-subject"
                            className="input"
                            value={subject}
                            maxLength={150}
                            onChange={(event) => setSubject(event.target.value)}
                        />

                        {role === 'admin' ? (
                            <>
                                <span className="field-label">Who receives it</span>
                                <div className="checks">
                                    <label>
                                        <input
                                            type="checkbox"
                                            checked={audience.includes('user')}
                                            onChange={() => toggle('user')}
                                        />
                                        Hosting accounts ({audiences.user ?? 0})
                                    </label>
                                    <label>
                                        <input
                                            type="checkbox"
                                            checked={audience.includes('reseller')}
                                            onChange={() => toggle('reseller')}
                                        />
                                        Resellers ({audiences.reseller ?? 0})
                                    </label>
                                </div>
                            </>
                        ) : (
                            <p className="muted">
                                Every hosting account you own receives this ({audiences.user ?? 0}).
                            </p>
                        )}

                        <label className="field-label" htmlFor="announcement-body">
                            Message
                        </label>
                        <textarea
                            id="announcement-body"
                            className="input"
                            style={{ minHeight: 180, lineHeight: 1.6 }}
                            maxLength={page?.max_body ?? 10000}
                            value={body}
                            onChange={(event) => setBody(event.target.value)}
                        />

                        <label className="checks">
                            <input type="checkbox" checked={email} onChange={() => setEmail(!email)} />
                            Also email it to them
                        </label>

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
