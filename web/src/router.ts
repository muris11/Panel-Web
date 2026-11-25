import { useEffect, useState } from 'react';
import type { MouseEvent } from 'react';

/**
 * A tiny path router. The panel has six screens, so react-router would be a
 * dependency that outweighs the problem; Go serves index.html for any path,
 * which is all a history router needs.
 */

const listeners = new Set<() => void>();

export function navigate(to: string, options: { replace?: boolean } = {}): void {
    if (to === window.location.pathname + window.location.search) {
        return;
    }

    if (options.replace) {
        window.history.replaceState({}, '', to);
    } else {
        window.history.pushState({}, '', to);
    }

    for (const listener of listeners) {
        listener();
    }
}

export function usePath(): string {
    const [path, setPath] = useState(() => window.location.pathname);

    useEffect(() => {
        const update = (): void => setPath(window.location.pathname);

        listeners.add(update);
        window.addEventListener('popstate', update);

        return () => {
            listeners.delete(update);
            window.removeEventListener('popstate', update);
        };
    }, []);

    return path;
}

/** Intercepts plain left clicks so a link stays an <a href>. */
export function linkProps(to: string): {
    href: string;
    onClick: (event: MouseEvent) => void;
} {
    return {
        href: to,
        onClick: (event: MouseEvent) => {
            if (event.defaultPrevented || event.button !== 0) {
                return;
            }

            if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
                return;
            }

            event.preventDefault();
            navigate(to);
        },
    };
}
