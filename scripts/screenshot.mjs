// Screenshots the running panel through the Chrome DevTools Protocol.
//
// Used during development to check what the panel actually looks like, and
// after a change to compare. It signs in once with the credentials given in
// MURISCORE_USER / MURISCORE_PASSWORD, then visits each screen and writes a PNG.
//
//   node scripts/screenshot.mjs
//
// Requires a panel listening on MURISCORE_BASE (default http://127.0.0.1:8080)
// and Chrome on the default Windows path.

import { mkdir, writeFile } from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { setTimeout as sleep } from 'node:timers/promises';

const BASE = process.env.MURISCORE_BASE ?? 'http://127.0.0.1:8080';
const USER = process.env.MURISCORE_USER ?? 'admin';
const PASSWORD = process.env.MURISCORE_PASSWORD ?? '';
const OUT = process.env.MURISCORE_SHOTS ?? '.shots';
const BROWSER =
    process.env.MURISCORE_BROWSER ?? 'C:/Program Files/Google/Chrome/Application/chrome.exe';

if (!PASSWORD) {
    console.error('set MURISCORE_PASSWORD to the panel account password');
    process.exit(2);
}

const screens = [
    { name: '02-overview', path: '/' },
    { name: '03-accounts', path: '/accounts' },
    { name: '04-activity', path: '/activity' },
    { name: '05-settings', path: '/settings' },
    { name: '06-domains', path: '/domains' },
    { name: '07-mail', path: '/mail' },
    { name: '08-databases', path: '/databases' },
    { name: '09-ftp', path: '/ftp' },
    { name: '10-files', path: '/files' },
    { name: '11-server', path: '/server' },
    { name: '12-domain-detail', path: '/domains/2' },
    { name: '13-account-files', path: '/accounts/siti/files' },
    { name: '14-account-cron', path: '/accounts/siti/cron' },
    { name: '15-account-backups', path: '/accounts/siti/backups' },
    { name: '26-account-ssh', path: '/accounts/siti/ssh' },
    { name: '27-tickets', path: '/tickets' },
    { name: '28-announcements', path: '/announcements' },
    { name: '29-packages', path: '/packages' },
    { name: '16-new-website', path: '/domains', click: 'New website' },
    { name: '17-new-mailbox', path: '/mail', click: 'Manage' },
    { name: '18-new-database', path: '/databases', click: 'New database' },
    { name: '19-new-ftp', path: '/ftp', click: 'New FTP login' },
    { name: '20-new-cron', path: '/accounts/siti/cron', click: 'New job' },
    { name: '21-two-factor', path: '/settings', click: 'Set up' },
    { name: '22-postgres', path: '/postgres' },
    { name: '23-redis', path: '/redis' },
    { name: '24-new-pg-login', path: '/postgres', click: 'New login' },
];

const PORT = 9333;
const endpoint = `http://127.0.0.1:${PORT}`;

const browser = spawn(
    BROWSER,
    [
        '--headless=new',
        `--remote-debugging-port=${PORT}`,
        `--user-data-dir=${process.cwd()}/.shots/profile`,
        '--hide-scrollbars',
        '--force-device-scale-factor=1',
        '--no-first-run',
        '--disable-gpu',
        'about:blank',
    ],
    { stdio: 'ignore' },
);

let socket;
let nextId = 1;
const pending = new Map();

function send(method, params = {}, sessionId) {
    const id = nextId++;
    socket.send(JSON.stringify({ id, method, params, sessionId }));

    return new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject });
    });
}

async function waitForBrowser() {
    for (let attempt = 0; attempt < 60; attempt += 1) {
        try {
            const response = await fetch(`${endpoint}/json/version`);
            return await response.json();
        } catch {
            await sleep(250);
        }
    }

    throw new Error('the browser did not start');
}

async function main() {
    await waitForBrowser();

    const targets = await (await fetch(`${endpoint}/json/list`)).json();
    const page = targets.find((target) => target.type === 'page');

    if (!page) {
        throw new Error('no page target to drive');
    }

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

            if (message.error) {
                reject(new Error(`${message.error.message}`));
            } else {
                resolve(message.result);
            }
        }
    });

    await send('Page.enable');
    await send('Runtime.enable');
    await send('Emulation.setDeviceMetricsOverride', {
        width: 1440,
        height: 900,
        deviceScaleFactor: 1,
        mobile: false,
    });

    await mkdir(`${OUT}/profile`, { recursive: true });

    // The first page has to be the panel's own origin, or the fetches below
    // resolve against about:blank.
    await send('Page.navigate', { url: `${BASE}/` });
    await sleep(1200);

    const anon = await send('Page.captureScreenshot', { format: 'png' });
    await writeFile(`${OUT}/01-login.png`, Buffer.from(anon.data, 'base64'));
    console.log('wrote', `${OUT}/01-login.png`);

    // Sign in through the page itself, so the session cookie is the one the
    // browser will send.
    const signIn = await send('Runtime.evaluate', {
        awaitPromise: true,
        returnByValue: true,
        expression: `(async () => {
            const probe = await fetch('/api/v1/auth/session').then((r) => r.json());
            const answer = await fetch('/api/v1/auth/login', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': probe.csrfToken },
                body: JSON.stringify({ login: ${JSON.stringify(USER)}, password: ${JSON.stringify(PASSWORD)} }),
            });
            const body = await answer.json();
            return { status: answer.status, body };
        })()`,
    });

    const result = signIn.result?.value;

    if (!result || result.status !== 200) {
        throw new Error(`sign in failed: ${result?.status} ${result?.body?.error?.message ?? ''}`);
    }

    console.log('signed in as', result.body.user.username);

    for (const screen of screens) {
        await send('Page.navigate', { url: `${BASE}${screen.path}` });
        await sleep(900);

        // A drawer is opened the way a person opens it: by clicking the button
        // that carries that label.
        if (screen.click) {
            await send('Runtime.evaluate', {
                awaitPromise: true,
                returnByValue: true,
                expression: `(async () => {
                    const label = ${JSON.stringify(screen.click)};
                    const target = [...document.querySelectorAll('button, a')].find(
                        (node) => node.textContent.trim() === label,
                    );
                    target?.click();
                    await new Promise((resolve) => setTimeout(resolve, 500));
                    return Boolean(target);
                })()`,
            });
        }

        const shot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
        await writeFile(`${OUT}/${screen.name}.png`, Buffer.from(shot.data, 'base64'));
        console.log('wrote', `${OUT}/${screen.name}.png`);
    }

    // Full-page shot of the overview, for checking long pages.
    await send('Page.navigate', { url: `${BASE}/` });
    await sleep(800);

    const metrics = await send('Page.getLayoutMetrics');
    const height = Math.ceil(metrics.cssContentSize.height);

    await send('Emulation.setDeviceMetricsOverride', {
        width: 1440,
        height,
        deviceScaleFactor: 1,
        mobile: false,
    });

    const full = await send('Page.captureScreenshot', { format: 'png' });
    await writeFile(`${OUT}/02-overview-full.png`, Buffer.from(full.data, 'base64'));
    console.log('wrote', `${OUT}/02-overview-full.png`);
}

try {
    await main();
} finally {
    socket?.close();
    browser.kill();
}
