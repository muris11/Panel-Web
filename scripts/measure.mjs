// Measures the rendered panel and prints anything that is clipped, off
// screen, or overlapping. Used after a layout change instead of guessing from
// a screenshot.
//
//   node scripts/measure.mjs
//
// It signs in the same way scripts/screenshot.mjs does and checks each screen
// at three widths.

import { spawn } from 'node:child_process';
import { setTimeout as sleep } from 'node:timers/promises';

const BASE = process.env.RAYXO_BASE ?? 'http://127.0.0.1:8080';
const USER = process.env.RAYXO_USER ?? 'admin';
const PASSWORD = process.env.RAYXO_PASSWORD ?? '';
const PORT = 9334;
const BROWSER =
    process.env.RAYXO_BROWSER ?? 'C:/Program Files/Google/Chrome/Application/chrome.exe';

if (!PASSWORD) {
    console.error('set RAYXO_PASSWORD to the panel account password');
    process.exit(2);
}

const screens = [
    '/',
    '/accounts',
    '/accounts/siti',
    '/accounts/siti/files',
    '/accounts/siti/cron',
    '/accounts/siti/backups',
    '/domains',
    '/domains/2',
    '/mail',
    '/databases',
    '/ftp',
    '/files',
    '/server',
    '/activity',
    '/settings',
];
const widths = [1440, 1024, 390];

const browser = spawn(
    BROWSER,
    [
        '--headless=new',
        `--remote-debugging-port=${PORT}`,
        `--user-data-dir=${process.cwd()}/.shots/profile`,
        '--hide-scrollbars',
        '--no-first-run',
        '--disable-gpu',
        'about:blank',
    ],
    { stdio: 'ignore' },
);

let socket;
let nextId = 1;
const pending = new Map();

function send(method, params = {}) {
    const id = nextId++;
    socket.send(JSON.stringify({ id, method, params }));

    return new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject });
    });
}

async function main() {
    for (let attempt = 0; attempt < 60; attempt += 1) {
        try {
            await fetch(`http://127.0.0.1:${PORT}/json/version`);
            break;
        } catch {
            await sleep(250);
        }
    }

    const targets = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
    const page = targets.find((target) => target.type === 'page');
    socket = new WebSocket(page.webSocketDebuggerUrl);

    await new Promise((resolve, reject) => {
        socket.addEventListener('open', resolve, { once: true });
        socket.addEventListener('error', reject, { once: true });
    });

    socket.addEventListener('message', (event) => {
        const message = JSON.parse(event.data);

        if (message.id && pending.has(message.id)) {
            const { resolve, reject } = pending.get(message.id);
            pending.delete(message.id);
            message.error ? reject(new Error(message.error.message)) : resolve(message.result);
        }
    });

    await send('Page.enable');
    await send('Runtime.enable');
    await send('Page.navigate', { url: `${BASE}/` });
    await sleep(1200);

    await send('Runtime.evaluate', {
        awaitPromise: true,
        expression: `(async () => {
            const probe = await fetch('/api/v1/auth/session').then((r) => r.json());
            await fetch('/api/v1/auth/login', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': probe.csrfToken },
                body: JSON.stringify({ login: ${JSON.stringify(USER)}, password: ${JSON.stringify(PASSWORD)} }),
            });
        })()`,
    });

    let problems = 0;

    for (const width of widths) {
        await send('Emulation.setDeviceMetricsOverride', {
            width,
            height: 900,
            deviceScaleFactor: 1,
            mobile: width < 600,
        });

        for (const screen of screens) {
            await send('Page.navigate', { url: `${BASE}${screen}` });
            await sleep(700);

            const audit = await send('Runtime.evaluate', {
                returnByValue: true,
                expression: `(() => {
                    const found = [];
                    const viewport = { w: innerWidth, h: innerHeight };

                    for (const element of document.querySelectorAll('body *')) {
                        const rect = element.getBoundingClientRect();
                        const style = getComputedStyle(element);

                        if (rect.width === 0 || rect.height === 0 || style.visibility === 'hidden') {
                            continue;
                        }

                        const name = element.tagName.toLowerCase() + (element.className && typeof element.className === 'string'
                            ? '.' + element.className.trim().split(/\\s+/).join('.')
                            : '');

                        if (rect.bottom > viewport.h + 1 && style.position === 'fixed') {
                            found.push({ kind: 'fixed-below-viewport', name, bottom: Math.round(rect.bottom) });
                        }

                        if (element.scrollWidth > element.clientWidth + 1 && style.overflowX === 'visible') {
                            found.push({ kind: 'content-wider-than-box', name, scrollWidth: element.scrollWidth, clientWidth: element.clientWidth });
                        }

                        if (style.overflow !== 'hidden' && element.children.length === 0 && element.scrollWidth > element.clientWidth + 1) {
                            found.push({ kind: 'text-overflow', name, text: (element.textContent || '').slice(0, 40) });
                        }
                    }

                    return {
                        overflowBody: document.documentElement.scrollWidth > viewport.w + 1,
                        scrollWidth: document.documentElement.scrollWidth,
                        viewport,
                        found: found.slice(0, 12),
                    };
                })()`,
            });

            const result = audit.result.value;
            const label = `${width}px ${screen}`;

            if (result.overflowBody) {
                problems += 1;
                console.log(`FAIL ${label}: page is ${result.scrollWidth}px wide in a ${result.viewport.w}px viewport`);
            }

            for (const issue of result.found) {
                problems += 1;
                console.log(`FAIL ${label}: ${issue.kind} on ${issue.name}`, issue.text ?? '');
            }

            if (!result.overflowBody && result.found.length === 0) {
                console.log(`ok   ${label}`);
            }
        }
    }

    console.log(problems === 0 ? '\nno layout problems found' : `\n${problems} layout problem(s)`);
    process.exitCode = problems === 0 ? 0 : 1;
}

try {
    await main();
} finally {
    socket?.close();
    browser.kill();
}
