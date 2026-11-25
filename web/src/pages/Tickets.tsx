import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';

import { api, when } from '../api';
import { Badge, Button, Card, CardHead, Drawer, Empty, errorMessage, SkeletonRows, statusTone } from '../ui';

interface Ticket {
    id: number;
    subject: string;
    status: string;
    priority: string;
    account: string;
    messages: number;
    opened_at: string;
    last_message_at: string | null;
}

interface Message {
    id: number;
    author: string;
    staff: boolean;
    body: string;
    created_at: string;
}

interface TicketList {
    box: string;
    status: string;
    tickets: Ticket[] | null;
    canAnswer: boolean;
    statuses: string[];
}

interface TicketDetail {
    ticket: Ticket;
    messages: Message[] | null;
    canAnswer: boolean;
}

/** Support tickets: an account opens one, an administrator or the reseller who
 *  owns the account answers it. */
export function Tickets({
    role,
    onToast,
}: {
    role: 'admin' | 'reseller' | 'user';
    onToast: (message: string, tone?: 'ok' | 'bad') => void;
}) {
    const [page, setPage] = useState<TicketList | null>(null);
    const [error, setError] = useState('');
    const [box, setBox] = useState(role === 'user' ? 'mine' : 'inbox');
    const [status, setStatus] = useState('');
    const [open, setOpen] = useState<TicketDetail | null>(null);
    const [composing, setComposing] = useState(false);
    const [subject, setSubject] = useState('');
    const [body, setBody] = useState('');
    const [priority, setPriority] = useState('normal');
    const [reply, setReply] = useState('');
    const [busy, setBusy] = useState('');
    const [formError, setFormError] = useState('');

    const load = useCallback(async () => {
        setError('');

        try {
            const query = new URLSearchParams({ box });
            if (status) {
                query.set('status', status);
            }

            setPage(await api<TicketList>(`/tickets?${query.toString()}`));
        } catch (problem) {
            setError(errorMessage(problem));
        }
    }, [box, status]);

    useEffect(() => {
        void load();
    }, [load]);

    const openTicket = async (id: number): Promise<void> => {
        try {
            setOpen(await api<TicketDetail>(`/tickets/${id}`));
            setReply('');
            setFormError('');
        } catch (problem) {
            onToast(errorMessage(problem), 'bad');
        }
    };

    const submitTicket = async (event: FormEvent): Promise<void> => {
        event.preventDefault();
        setBusy('open');
        setFormError('');

        try {
            const created = await api<{ id: number }>('/tickets', {
                method: 'POST',
                body: { subject, body, priority },
            });

            setComposing(false);
            setSubject('');
            setBody('');
            setPriority('normal');
            onToast('The ticket is open.');
            await load();
            await openTicket(created.id);
        } catch (problem) {
            setFormError(errorMessage(problem));
        } finally {
            setBusy('');
        }
    };

    const sendReply = async (event: FormEvent): Promise<void> => {
        event.preventDefault();
        if (!open) {
            return;
        }

        setBusy('reply');
        setFormError('');

        try {
            await api(`/tickets/${open.ticket.id}/reply`, {
                method: 'POST',
                body: { body: reply, reopen: open.ticket.status === 'closed' },
            });

            setReply('');
            await openTicket(open.ticket.id);
            await load();
            onToast('The reply is saved.');
        } catch (problem) {
            setFormError(errorMessage(problem));
        } finally {
            setBusy('');
        }
    };

    const setTicketStatus = async (next: 'open' | 'closed'): Promise<void> => {
        if (!open) {
            return;
        }

        setBusy(next);

        try {
            await api(`/tickets/${open.ticket.id}/status`, { method: 'POST', body: { status: next } });
            await openTicket(open.ticket.id);
            await load();
            onToast(next === 'closed' ? 'The ticket is closed.' : 'The ticket is open again.');
        } catch (problem) {
            onToast(errorMessage(problem), 'bad');
        } finally {
            setBusy('');
        }
    };

    const answers = page?.canAnswer ?? false;

    return (
        <>
            <div className="page-head">
                <div>
                    <h1 className="page-title">Tickets</h1>
                    <p className="page-sub">
                        {answers
                            ? 'Questions from the accounts you answer. A reply moves the ticket back to them.'
                            : 'Ask for help with your hosting. Replies arrive here and by email.'}
                    </p>
                </div>
                <Button variant="primary" onClick={() => setComposing(true)}>
                    New ticket
                </Button>
            </div>

            {error ? (
                <div className="banner bad" role="alert">
                    {error}
                </div>
            ) : null}

            <Card>
                <CardHead title={box === 'inbox' ? 'Inbox' : 'Your tickets'}>
                    <div className="row">
                        {answers ? (
                            <>
                                <Button size="sm" variant={box === 'inbox' ? 'primary' : 'default'} onClick={() => setBox('inbox')}>
                                    Inbox
                                </Button>
                                <Button size="sm" variant={box === 'mine' ? 'primary' : 'default'} onClick={() => setBox('mine')}>
                                    Mine
                                </Button>
                            </>
                        ) : null}
                        <select
                            className="input"
                            value={status}
                            onChange={(event) => setStatus(event.target.value)}
                            aria-label="Filter by status"
                        >
                            <option value="">Any status</option>
                            <option value="open">Open</option>
                            <option value="answered">Answered</option>
                            <option value="closed">Closed</option>
                        </select>
                    </div>
                </CardHead>

                {page === null ? (
                    <SkeletonRows rows={4} columns={4} />
                ) : (page.tickets ?? []).length === 0 ? (
                    <Empty title={box === 'inbox' ? 'Nothing waiting' : 'No tickets yet'}>
                        {box === 'inbox'
                            ? 'When an account asks for help, the ticket shows up here.'
                            : 'Open one when something is not working.'}
                    </Empty>
                ) : (
                    <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Subject</th>
                                {answers && box === 'inbox' ? <th>Account</th> : null}
                                <th>Status</th>
                                <th>Priority</th>
                                <th>Replies</th>
                                <th>Last activity</th>
                            </tr>
                        </thead>
                        <tbody>
                            {(page.tickets ?? []).map((ticket) => (
                                <tr key={ticket.id} className="clickable" onClick={() => void openTicket(ticket.id)}>
                                    <td>{ticket.subject}</td>
                                    {answers && box === 'inbox' ? <td className="mono">{ticket.account}</td> : null}
                                    <td>
                                        <Badge tone={statusTone(ticket.status)}>{ticket.status}</Badge>
                                    </td>
                                    <td>{ticket.priority}</td>
                                    <td>{ticket.messages}</td>
                                    <td>{when(ticket.last_message_at ?? ticket.opened_at)}</td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                    </div>
                )}
            </Card>

            {composing ? (
                <Drawer
                    title="New ticket"
                    subtitle="Describe what happened; the more detail, the faster the answer."
                    onClose={() => setComposing(false)}
                    footer={
                        <>
                            <Button onClick={() => setComposing(false)}>Cancel</Button>
                            <Button variant="primary" onClick={(event) => void submitTicket(event)} busy={busy === 'open'}>
                                Open the ticket
                            </Button>
                        </>
                    }
                >
                    <form className="form" onSubmit={(event) => void submitTicket(event)}>
                        <label className="field-label" htmlFor="ticket-subject">
                            Subject
                        </label>
                        <input
                            id="ticket-subject"
                            className="input"
                            value={subject}
                            maxLength={150}
                            onChange={(event) => setSubject(event.target.value)}
                        />
                        <label className="field-label" htmlFor="ticket-priority">
                            Priority
                        </label>
                        <select
                            id="ticket-priority"
                            className="input"
                            value={priority}
                            onChange={(event) => setPriority(event.target.value)}
                        >
                            <option value="low">Low</option>
                            <option value="normal">Normal</option>
                            <option value="high">High</option>
                        </select>
                        <label className="field-label" htmlFor="ticket-body">
                            What happened
                        </label>
                        <textarea
                            id="ticket-body"
                            className="input"
                            rows={8}
                            maxLength={10000}
                            value={body}
                            onChange={(event) => setBody(event.target.value)}
                        />
                        {formError ? (
                            <p className="field-error" role="alert">
                                {formError}
                            </p>
                        ) : null}
                    </form>
                </Drawer>
            ) : null}

            {open ? (
                <Drawer
                    title={open.ticket.subject}
                    subtitle={`${open.ticket.account} · opened ${when(open.ticket.opened_at)}`}
                    onClose={() => setOpen(null)}
                    footer={
                        open.canAnswer && open.ticket.status === 'closed' ? (
                            <Button onClick={() => void setTicketStatus('open')} busy={busy === 'open'}>
                                Reopen
                            </Button>
                        ) : open.canAnswer ? (
                            <Button variant="danger" onClick={() => void setTicketStatus('closed')} busy={busy === 'closed'}>
                                Close the ticket
                            </Button>
                        ) : null
                    }
                >
                    <div className="stack">
                        <div className="row">
                            <Badge tone={statusTone(open.ticket.status)}>{open.ticket.status}</Badge>
                            <Badge tone="plain">{open.ticket.priority}</Badge>
                        </div>

                        {(open.messages ?? []).map((message) => (
                            <div key={message.id} className={`bubble ${message.staff ? 'staff' : ''}`.trim()}>
                                <div className="bubble-head">
                                    <strong>{message.author || 'Deleted account'}</strong>
                                    {message.staff ? <span className="muted">staff</span> : null}
                                    <span className="muted">{when(message.created_at)}</span>
                                </div>
                                <p className="bubble-body">{message.body}</p>
                            </div>
                        ))}

                        <form className="form" onSubmit={(event) => void sendReply(event)}>
                            <label className="field-label" htmlFor="ticket-reply">
                                {open.ticket.status === 'closed' ? 'Reopen with a reply' : 'Reply'}
                            </label>
                            <textarea
                                id="ticket-reply"
                                className="input"
                                rows={5}
                                maxLength={10000}
                                value={reply}
                                onChange={(event) => setReply(event.target.value)}
                                placeholder="Add what you found, or a question."
                            />
                            {formError ? (
                                <p className="field-error" role="alert">
                                    {formError}
                                </p>
                            ) : null}
                            <div className="row end">
                                <Button type="submit" variant="primary" busy={busy === 'reply'} disabled={reply.trim() === ''}>
                                    Send
                                </Button>
                            </div>
                        </form>
                    </div>
                </Drawer>
            ) : null}
        </>
    );
}
