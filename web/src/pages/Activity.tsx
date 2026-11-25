import { useCallback, useEffect, useState } from 'react';
import { Fragment } from 'react';

import { actionLabel, api, when, type Task } from '../api';
import { Badge, Card, Empty, errorMessage, SkeletonRows, statusTone } from '../ui';

const FILTERS = ['all', 'queued', 'running', 'success', 'failed'] as const;

type Filter = (typeof FILTERS)[number];

/** The activity log: every agent call the panel has queued, newest first. */
export function Activity() {
    const [filter, setFilter] = useState<Filter>('all');
    const [tasks, setTasks] = useState<Task[] | null>(null);
    const [error, setError] = useState('');
    const [expanded, setExpanded] = useState<string | null>(null);

    const load = useCallback(async () => {
        try {
            const query = filter === 'all' ? '' : `&status=${filter}`;
            const response = await api<{ tasks: Task[] }>(`/tasks?limit=100${query}`);
            setTasks(response.tasks);
            setError('');
        } catch (thrown) {
            setError(errorMessage(thrown));
        }
    }, [filter]);

    useEffect(() => {
        setTasks(null);
        void load();

        // A task is a background job: while any are still running, keep the
        // list current without asking the operator to refresh.
        const timer = window.setInterval(() => void load(), 4000);

        return () => window.clearInterval(timer);
    }, [load]);

    return (
        <div className="page">
            <header className="page-head">
                <div>
                    <h1 className="page-title">Activity</h1>
                    <p className="page-sub">Everything the panel has asked the server agent to do</p>
                </div>
                <div className="row" role="tablist" aria-label="Filter by status">
                    {FILTERS.map((entry) => (
                        <button
                            key={entry}
                            role="tab"
                            aria-selected={filter === entry}
                            className={`btn sm ${filter === entry ? '' : 'ghost'}`}
                            onClick={() => setFilter(entry)}
                        >
                            {entry}
                        </button>
                    ))}
                </div>
            </header>

            {error ? <div className="banner bad">{error}</div> : null}

            <Card>
                <div className="table-wrap">
                    <table>
                        <thead>
                            <tr>
                                <th>Action</th>
                                <th>Account</th>
                                <th>Status</th>
                                <th className="num">Attempts</th>
                                <th>Queued</th>
                                <th>Finished</th>
                            </tr>
                        </thead>

                        {tasks === null ? (
                            <SkeletonRows rows={6} columns={6} />
                        ) : (
                            <tbody>
                                {tasks.map((task) => (
                                    <Fragment key={task.id}>
                                        <tr
                                            onClick={() => setExpanded(expanded === task.id ? null : task.id)}
                                            style={{ cursor: task.error || task.output ? 'pointer' : 'default' }}
                                        >
                                            <td className="strong">{actionLabel(task.action)}</td>
                                            <td className="mono muted">{task.username ?? 'server'}</td>
                                            <td>
                                                <Badge tone={statusTone(task.status)}>{task.status}</Badge>
                                            </td>
                                            <td className="num muted">{task.attempts ?? 0}</td>
                                            <td className="muted">{when(task.createdAt)}</td>
                                            <td className="muted">{when(task.finishedAt)}</td>
                                        </tr>

                                        {expanded === task.id && (task.error || task.output) ? (
                                            <tr>
                                                <td colSpan={6} style={{ background: 'var(--ink-800)' }}>
                                                    {task.error ? (
                                                        <div className="banner bad" style={{ marginBottom: task.output ? 8 : 0 }}>
                                                            {task.error}
                                                        </div>
                                                    ) : null}
                                                    {task.output ? (
                                                        <pre
                                                            className="mono"
                                                            style={{
                                                                margin: 0,
                                                                whiteSpace: 'pre-wrap',
                                                                color: 'var(--text-2)',
                                                                maxHeight: 260,
                                                                overflow: 'auto',
                                                            }}
                                                        >
                                                            {pretty(task.output)}
                                                        </pre>
                                                    ) : null}
                                                </td>
                                            </tr>
                                        ) : null}
                                    </Fragment>
                                ))}
                            </tbody>
                        )}
                    </table>
                </div>

                {tasks !== null && tasks.length === 0 ? (
                    <Empty title="Nothing to show">
                        {filter === 'all'
                            ? 'Create an account or change a website and its work appears here.'
                            : `No task is ${filter} right now.`}
                    </Empty>
                ) : null}
            </Card>
        </div>
    );
}

/** Agent output is JSON when it is structured and plain text when it is not;
 *  showing the JSON indented beats showing one long line. */
function pretty(output: string): string {
    try {
        return JSON.stringify(JSON.parse(output), null, 2);
    } catch {
        return output;
    }
}
