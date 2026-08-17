import { createServer } from 'vite';

const ownerPID = process.ppid;
const server = await createServer({
  server: { host: '127.0.0.1', port: 4187, strictPort: true },
});
let closing = false;

async function close() {
  if (closing) return;
  closing = true;
  clearInterval(ownerWatch);
  await server.close();
  process.exit(0);
}

process.once('SIGINT', close);
process.once('SIGTERM', close);
process.once('SIGHUP', close);

const ownerWatch = setInterval(() => {
  try {
    process.kill(ownerPID, 0);
  } catch {
    void close();
  }
}, 100);
ownerWatch.unref();

await server.listen();
