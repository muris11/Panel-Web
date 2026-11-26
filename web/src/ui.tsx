import { useEffect, useId, useRef, useState } from 'react';
import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode } from 'react';

import { ApiError } from './api';

/* ------------------------------------------------------------------ icons */

type IconProps = { className?: string };

const stroke = {
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 1.6,
    strokeLinecap: 'round' as const,
    strokeLinejoin: 'round' as const,
};

export function IconGauge({ className = 'nav-icon' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} aria-hidden="true" {...stroke}>
            <path d="M12 20a8 8 0 1 1 8-8" />
            <path d="M12 12l4-3.5" />
            <path d="M4 20h16" />
        </svg>
    );
}

export function IconUsers({ className = 'nav-icon' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} aria-hidden="true" {...stroke}>
            <circle cx="9" cy="8" r="3.2" />
            <path d="M3.5 19.5c.6-3 2.8-4.6 5.5-4.6s4.9 1.6 5.5 4.6" />
            <path d="M16.5 7.2a3 3 0 0 1 0 5.6" />
            <path d="M18 15.4c1.6.7 2.6 2.1 2.9 4.1" />
        </svg>
    );
}

export function IconPause({ className = 'nav-icon' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} aria-hidden="true" {...stroke}>
            <path d="M9.5 5.5v13M14.5 5.5v13" />
        </svg>
    );
}

export function IconActivity({ className = 'nav-icon' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} aria-hidden="true" {...stroke}>
            <path d="M3 12h3.5l2.5-6 3.5 12 2.5-6H21" />
        </svg>
    );
}

export function IconGlobe({ className = 'nav-icon' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} aria-hidden="true" {...stroke}>
            <circle cx="12" cy="12" r="8.5" />
            <path d="M3.5 12h17M12 3.5c2.4 2.4 3.6 5.4 3.6 8.5S14.4 18.1 12 20.5c-2.4-2.4-3.6-5.4-3.6-8.5S9.6 5.9 12 3.5Z" />
        </svg>
    );
}

export function IconMail({ className = 'nav-icon' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} aria-hidden="true" {...stroke}>
            <rect x="3" y="5.5" width="18" height="13" rx="2" />
            <path d="m4 7.5 8 5.5 8-5.5" />
        </svg>
    );
}

export function IconDatabase({ className = 'nav-icon' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} aria-hidden="true" {...stroke}>
            <ellipse cx="12" cy="6" rx="7.5" ry="3" />
            <path d="M4.5 6v6c0 1.7 3.4 3 7.5 3s7.5-1.3 7.5-3V6M4.5 12v6c0 1.7 3.4 3 7.5 3s7.5-1.3 7.5-3v-6" />
        </svg>
    );
}

export function IconFolder({ className = 'nav-icon' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} aria-hidden="true" {...stroke}>
            <path d="M3.5 7.5A1.5 1.5 0 0 1 5 6h4l2 2.5h8a1.5 1.5 0 0 1 1.5 1.5v7.5A1.5 1.5 0 0 1 19 19H5a1.5 1.5 0 0 1-1.5-1.5Z" />
        </svg>
    );
}

export function IconServer({ className = 'nav-icon' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} aria-hidden="true" {...stroke}>
            <rect x="3.5" y="4" width="17" height="6.5" rx="1.5" />
            <rect x="3.5" y="13.5" width="17" height="6.5" rx="1.5" />
            <path d="M7 7.25h.01M7 16.75h.01" />
        </svg>
    );
}

export function IconSettings({ className = 'nav-icon' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} aria-hidden="true" {...stroke}>
            <circle cx="12" cy="12" r="3" />
            <path d="M12 3v2.2M12 18.8V21M3 12h2.2M18.8 12H21M5.6 5.6l1.6 1.6M16.8 16.8l1.6 1.6M18.4 5.6l-1.6 1.6M7.2 16.8l-1.6 1.6" />
        </svg>
    );
}

export function IconLogout({ className = 'nav-icon' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} aria-hidden="true" {...stroke}>
            <path d="M15 4h3.5A1.5 1.5 0 0 1 20 5.5v13a1.5 1.5 0 0 1-1.5 1.5H15" />
            <path d="M10 8l-4 4 4 4M6 12h9" />
        </svg>
    );
}

export function IconKey({ className = 'nav-icon' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} aria-hidden="true" {...stroke}>
            <circle cx="8" cy="15" r="4" />
            <path d="M10.8 12.2 20 3M16 7l3 3M13 10l2.5 2.5" />
        </svg>
    );
}

export function IconPackage({ className = 'nav-icon' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} aria-hidden="true" {...stroke}>
            <path d="M12 3 20 7v10l-8 4-8-4V7z" />
            <path d="M4 7l8 4 8-4M12 11v10" />
        </svg>
    );
}

export function IconTicket({ className = 'nav-icon' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} aria-hidden="true" {...stroke}>
            <path d="M4 9V7a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v2a3 3 0 0 0 0 6v2a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2v-2a3 3 0 0 0 0-6z" />
            <path d="M13 5v2M13 11v2M13 17v2" />
        </svg>
    );
}

export function IconMegaphone({ className = 'nav-icon' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} aria-hidden="true" {...stroke}>
            <path d="M4 10v4a1 1 0 0 0 1 1h2l6 4V5L7 9H5a1 1 0 0 0-1 1z" />
            <path d="M17 8.5a5 5 0 0 1 0 7" />
        </svg>
    );
}

export function IconPlus({ className = '' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} width="15" height="15" aria-hidden="true" {...stroke}>
            <path d="M12 5v14M5 12h14" />
        </svg>
    );
}

export function IconClose({ className = '' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} width="15" height="15" aria-hidden="true" {...stroke}>
            <path d="M6 6l12 12M18 6L6 18" />
        </svg>
    );
}

export function IconBack({ className = '' }: IconProps) {
    return (
        <svg viewBox="0 0 24 24" className={className} width="15" height="15" aria-hidden="true" {...stroke}>
            <path d="M14 6l-6 6 6 6" />
        </svg>
    );
}

/* ------------------------------------------------------------- primitives */

export function Card({ children, className = '' }: { children: ReactNode; className?: string }) {
    return <section className={`card ${className}`.trim()}>{children}</section>;
}

export function CardHead({ title, children }: { title: ReactNode; children?: ReactNode }) {
    return (
        <header className="card-head">
            <span className="card-title">{title}</span>
            {children}
        </header>
    );
}

export function Badge({
    tone = 'plain',
    children,
}: {
    tone?: 'ok' | 'warn' | 'bad' | 'info' | 'plain';
    children: ReactNode;
}) {
    return <span className={`badge ${tone}`}>{children}</span>;
}

export function statusTone(status: string): 'ok' | 'warn' | 'bad' | 'info' | 'plain' {
    switch (status) {
        case 'active':
        case 'success':
        case 'succeeded': // written by an earlier build of this panel
            return 'ok';
        case 'queued':
        case 'running':
            return 'info';
        case 'suspended':
        case 'failed':
            return 'bad';
        case 'deleting':
        case 'cancelled':
            return 'warn';
        case 'pending':
            return 'warn';
        default:
            return 'plain';
    }
}

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
    variant?: 'default' | 'primary' | 'ghost' | 'danger' | 'ghost danger';
    size?: 'md' | 'sm';
    busy?: boolean;
}

export function Button({
    variant = 'default',
    size = 'md',
    busy = false,
    disabled,
    children,
    className = '',
    ...rest
}: ButtonProps) {
    return (
        <button
            {...rest}
            disabled={disabled || busy}
            className={`btn ${variant === 'default' ? '' : variant.replace(' ', ' ')} ${size === 'sm' ? 'sm' : ''} ${className}`.trim()}
        >
            {busy ? <span className="spinner" aria-hidden="true" /> : null}
            {children}
        </button>
    );
}

interface FieldProps extends InputHTMLAttributes<HTMLInputElement> {
    label: string;
    error?: string;
    hint?: string;
    span?: boolean;
}

export function Field({ label, error, hint, span = false, id, ...rest }: FieldProps) {
    const generated = useId();
    const inputId = id ?? generated;

    return (
        <div className={`field ${span ? 'span-2' : ''}`.trim()}>
            <label className="field-label" htmlFor={inputId}>
                {label}
            </label>
            <input
                {...rest}
                id={inputId}
                className={`input ${error ? 'has-error' : ''}`.trim()}
                aria-invalid={error ? true : undefined}
                aria-describedby={error ? `${inputId}-error` : undefined}
            />
            {error ? (
                <p className="field-error" id={`${inputId}-error`}>
                    {error}
                </p>
            ) : null}
            {hint && !error ? <span className="field-hint">{hint}</span> : null}
        </div>
    );
}

/** Shown instead of a table when there is nothing to list, and it says what
 *  to do next rather than only that the list is empty. */
export function Empty({ title, children }: { title: string; children?: ReactNode }) {
    return (
        <div className="empty">
            <span className="empty-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24" width="18" height="18" {...stroke}>
                    <path d="M4 13h4l2 3h4l2-3h4" />
                    <path d="M6.5 6.5 4 13v5.5h16V13l-2.5-6.5z" />
                </svg>
            </span>
            <strong>{title}</strong>
            {children}
        </div>
    );
}

export function TableFoot({ children }: { children: ReactNode }) {
    return <div className="table-foot">{children}</div>;
}

export function SkeletonRows({ rows = 4, columns = 4 }: { rows?: number; columns?: number }) {
    return (
        <tbody>
            {Array.from({ length: rows }, (_, row) => (
                <tr key={row}>
                    {Array.from({ length: columns }, (_, column) => (
                        <td key={column}>
                            <span
                                className="skeleton"
                                style={{ display: 'block', height: 14, width: column === 0 ? '60%' : '38%' }}
                            >
                                .
                            </span>
                        </td>
                    ))}
                </tr>
            ))}
        </tbody>
    );
}

export function Meter({
    value,
    ceiling,
    tone = 'ok',
}: {
    value: number;
    ceiling: number;
    tone?: 'ok' | 'warn' | 'bad';
}) {
    const percent = ceiling > 0 ? Math.min(100, Math.round((value / ceiling) * 100)) : 0;
    const color = tone === 'bad' ? 'var(--red)' : tone === 'warn' ? 'var(--amber)' : 'var(--accent)';

    return (
        <div className="meter" role="progressbar" aria-valuenow={percent} aria-valuemin={0} aria-valuemax={100}>
            <span style={{ width: `${percent}%`, background: color }} />
        </div>
    );
}

/* ------------------------------------------------------------------ drawer */

export function Drawer({
    title,
    subtitle,
    onClose,
    footer,
    children,
}: {
    title: string;
    subtitle?: string;
    onClose: () => void;
    footer?: ReactNode;
    children: ReactNode;
}) {
    const panel = useRef<HTMLDivElement>(null);

    useEffect(() => {
        const onKey = (event: KeyboardEvent): void => {
            if (event.key === 'Escape') {
                onClose();
            }
        };

        document.addEventListener('keydown', onKey);
        panel.current?.focus();

        return () => document.removeEventListener('keydown', onKey);
    }, [onClose]);

    return (
        <div
            className="overlay"
            onMouseDown={(event) => {
                if (event.target === event.currentTarget) {
                    onClose();
                }
            }}
        >
            <div className="drawer" role="dialog" aria-modal="true" aria-label={title} ref={panel} tabIndex={-1}>
                <div className="drawer-head">
                    <div>
                        <div className="card-title">{title}</div>
                        {subtitle ? <p className="page-sub">{subtitle}</p> : null}
                    </div>
                    <button className="btn-icon" onClick={onClose} aria-label="Close">
                        <IconClose />
                    </button>
                </div>
                <div className="drawer-body">{children}</div>
                {footer ? <div className="drawer-foot">{footer}</div> : null}
            </div>
        </div>
    );
}

/* ------------------------------------------------------------------ toasts */

export interface Toast {
    id: number;
    tone: 'ok' | 'bad';
    message: string;
}

export function useToasts() {
    const [toasts, setToasts] = useState<Toast[]>([]);
    const next = useRef(1);

    const push = (message: string, tone: 'ok' | 'bad' = 'ok'): void => {
        const id = next.current++;
        setToasts((current) => [...current, { id, tone, message }]);

        window.setTimeout(() => {
            setToasts((current) => current.filter((toast) => toast.id !== id));
        }, 5200);
    };

    const view = (
        <div className="toasts" aria-live="polite">
            {toasts.map((toast) => (
                <div key={toast.id} className={`toast ${toast.tone}`}>
                    {toast.message}
                </div>
            ))}
        </div>
    );

    return { push, view };
}

/** Turns any thrown value into something worth showing an operator. */
export function errorMessage(error: unknown): string {
    if (error instanceof ApiError) {
        return error.message;
    }

    if (error instanceof Error) {
        return error.message;
    }

    return 'Something went wrong.';
}
