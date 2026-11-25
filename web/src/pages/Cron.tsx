import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';

import { api, type CronJobRow } from '../api';
import { linkProps } from '../router';
import { Badge, Button, Card, CardHead, Drawer, Empty, errorMessage, Field, SkeletonRows } from '../ui';

export function Cron({ username, onToast }: { username: string; onToast: (message: string, tone?: 'ok' | 'bad') => void }) {
    const [jobs, setJobs] = useState<CronJobRow[] | null>(null);
    const [mailTo, setMailTo] = useState('');
    const [error, setError] = useState('');
    const [editing, setEditing] = useState<CronJobRow | 'new' | null>(null);
    const [busy, setBusy] = useState('');

    const load = useCallback(async () => {
        try {
            const response = await api<{ jobs: CronJobRow[]; mailTo: string }>(
                `/accounts/${encodeURIComponent(username)}/cron`,
            );

            setJobs(response.jobs);
            setMailTo(response.mailTo);
            setError('');
        } catch (thrown) {
            setError(errorMessage(thrown));
        }
    }, [username]);

    useEffect(() => {
        void load();
    }, [load]);

    async function remove(job: CronJobRow): Promise<void> {
        if (!window.confirm(`Delete the job "${job.command}"?`)) {
            return;
        }

        setBusy(String(job.id));

        try {
            await api(`/cron/${job.id}`, { method: 'DELETE' });
            onToast('The job was removed');
            await load();
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
                    <div className="crumbs">
                        <a {...linkProps(`/accounts/${username}`)}>{username}</a>
                        <span>/</span>
                        <span>cron</span>
                    </div>
                    <h1 className="page-title">Cron jobs</h1>
                    <p className="page-sub">
                        {jobs === null ? 'Loading' : `${jobs.length} job${jobs.length === 1 ? '' : 's'}`}
                        {mailTo ? ` · output goes to ${mailTo}` : ''}
                    </p>
                </div>
                <Button variant="primary" onClick={() => setEditing('new')}>
                    New job
                </Button>
            </header>

            {error ? <div className="banner bad">{error}</div> : null}

            <Card>
                <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Schedule</th>
                                <th>Command</th>
                                <th>Description</th>
                                <th>State</th>
                                <th style={{ textAlign: 'right' }}>Actions</th>
                            </tr>
                        </thead>

                        {jobs === null ? (
                            <SkeletonRows rows={3} columns={5} />
                        ) : jobs.length === 0 ? null : (
                            <tbody>
                                {jobs.map((job) => (
                                    <tr key={job.id}>
                                        <td className="mono strong">{job.schedule}</td>
                                        <td className="mono" style={{ wordBreak: 'break-all' }}>
                                            {job.command}
                                        </td>
                                        <td className="muted">{job.description || '—'}</td>
                                        <td>
                                            <Badge tone={job.enabled ? 'ok' : 'plain'}>{job.enabled ? 'enabled' : 'paused'}</Badge>
                                        </td>
                                        <td>
                                            <div className="row" style={{ justifyContent: 'flex-end', gap: 6 }}>
                                                <Button size="sm" onClick={() => setEditing(job)}>
                                                    Edit
                                                </Button>
                                                <Button size="sm" busy={busy === String(job.id)} onClick={() => void remove(job)}>
                                                    Delete
                                                </Button>
                                            </div>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        )}
                    </table>
                </div>

                {jobs !== null && jobs.length === 0 ? (
                    <Empty title="No cron jobs yet">
                        A job runs as {username}, so it can only touch that account's files and databases.
                    </Empty>
                ) : null}
            </Card>

            {editing ? (
                <JobDrawer
                    username={username}
                    job={editing === 'new' ? null : editing}
                    onClose={() => setEditing(null)}
                    onSaved={() => {
                        setEditing(null);
                        onToast('The crontab will be updated');
                        void load();
                    }}
                />
            ) : null}
        </div>
    );
}

function JobDrawer({
    username,
    job,
    onClose,
    onSaved,
}: {
    username: string;
    job: CronJobRow | null;
    onClose: () => void;
    onSaved: () => void;
}) {
    const [schedule, setSchedule] = useState(job?.schedule ?? '0 3 * * *');
    const [command, setCommand] = useState(job?.command ?? '');
    const [description, setDescription] = useState(job?.description ?? '');
    const [enabled, setEnabled] = useState(job?.enabled ?? true);
    const [fields, setFields] = useState<Record<string, string>>({});
    const [error, setError] = useState('');
    const [busy, setBusy] = useState(false);

    async function save(event: FormEvent): Promise<void> {
        event.preventDefault();
        setBusy(true);
        setError('');
        setFields({});

        const body = { schedule, command, description, enabled };

        try {
            if (job) {
                await api(`/cron/${job.id}`, { method: 'PATCH', body });
            } else {
                await api(`/accounts/${encodeURIComponent(username)}/cron`, { method: 'POST', body });
            }

            onSaved();
        } catch (thrown) {
            setError(errorMessage(thrown));
            setFields((thrown as { fields?: Record<string, string> }).fields ?? {});
        } finally {
            setBusy(false);
        }
    }

    return (
        <Drawer
            title={job ? 'Edit job' : 'New job'}
            subtitle="Five fields: minute, hour, day, month, weekday. @daily and friends also work."
            onClose={onClose}
            footer={
                <>
                    <Button onClick={onClose}>Cancel</Button>
                    <Button variant="primary" type="submit" form="cron-form" busy={busy}>
                        Save job
                    </Button>
                </>
            }
        >
            {error ? <div className="banner bad">{error}</div> : null}

            <form id="cron-form" className="form" onSubmit={(event) => void save(event)}>
                <Field
                    label="Schedule"
                    value={schedule}
                    onChange={(event) => setSchedule(event.target.value)}
                    placeholder="*/5 * * * *"
                    error={fields.schedule}
                    required
                />

                <Field
                    label="Command"
                    value={command}
                    onChange={(event) => setCommand(event.target.value)}
                    placeholder="php /home/account/public_html/artisan schedule:run"
                    error={fields.command}
                    required
                />

                <Field
                    label="Description"
                    value={description}
                    onChange={(event) => setDescription(event.target.value)}
                    placeholder="What this job does"
                    error={fields.description}
                />

                <label className="check">
                    <input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />
                    Run this job
                </label>
            </form>
        </Drawer>
    );
}

/** Cron for an account picked from a list, for links that carry no username. */
export { CardHead };
