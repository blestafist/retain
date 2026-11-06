import { createServer as createHttpServer, request as httpRequest, type Server } from 'node:http';
import { fileURLToPath } from 'node:url';
import { createServer as createViteServer } from 'vite';
import { describe, expect, it } from 'vitest';
import config from '../../vite.config';

describe('Vite development API proxy', () => {
  it('forwards the path, query, Host, and Origin through the running dev server', async () => {
    let receivedRequest:
      | { url: string | undefined; host: string | undefined; origin: string | undefined }
      | undefined;
    const backend = createHttpServer((request, response) => {
      receivedRequest = {
        url: request.url,
        host: request.headers.host,
        origin: request.headers.origin,
      };
      response.writeHead(204).end();
    });
    let vite: Awaited<ReturnType<typeof createViteServer>> | undefined;

    try {
      const backendPort = await listenOnEphemeralPort(backend);
      const configuredProxy = config.server?.proxy;
      const apiProxy = configuredProxy?.['/api'];
      if (!configuredProxy || !apiProxy || typeof apiProxy === 'string') {
        throw new Error('Expected vite.config.ts to define an /api proxy object');
      }

      vite = await createViteServer({
        configFile: false,
        root: fileURLToPath(new URL('../..', import.meta.url)),
        plugins: config.plugins,
        server: {
          ...config.server,
          host: 'localhost',
          port: 0,
          strictPort: true,
          proxy: {
            ...configuredProxy,
            '/api': { ...apiProxy, target: `http://127.0.0.1:${backendPort}` },
          },
        },
      });
      await vite.listen();

      const address = vite.httpServer?.address();
      if (!address || typeof address === 'string') {
        throw new Error('Expected Vite to listen on a TCP port');
      }

      const origin = 'http://localhost:5173';
      const statusCode = await requestThroughVite(address.port, '/api/v1/me?x=1', origin);

      expect(statusCode).toBe(204);
      expect(receivedRequest).toEqual({
        url: '/api/v1/me?x=1',
        host: `localhost:${address.port}`,
        origin,
      });
    } finally {
      try {
        await vite?.close();
      } finally {
        await closeHttpServer(backend);
      }
    }
  });
});

function listenOnEphemeralPort(server: Server): Promise<number> {
  return new Promise((resolve, reject) => {
    const onError = (error: Error) => {
      server.off('listening', onListening);
      reject(error);
    };
    const onListening = () => {
      server.off('error', onError);
      const address = server.address();
      if (!address || typeof address === 'string') {
        reject(new Error('Expected the test backend to listen on a TCP port'));
        return;
      }
      resolve(address.port);
    };

    server.once('error', onError);
    server.once('listening', onListening);
    server.listen(0, '127.0.0.1');
  });
}

function requestThroughVite(port: number, path: string, origin: string): Promise<number | undefined> {
  return new Promise((resolve, reject) => {
    const request = httpRequest(
      `http://localhost:${port}${path}`,
      { headers: { origin } },
      (response) => {
        response.on('error', reject);
        response.on('end', () => resolve(response.statusCode));
        response.resume();
      },
    );

    request.setTimeout(5_000, () => request.destroy(new Error('Timed out waiting for Vite proxy')));
    request.on('error', reject);
    request.end();
  });
}

function closeHttpServer(server: Server): Promise<void> {
  return new Promise((resolve, reject) => {
    if (!server.listening) {
      resolve();
      return;
    }

    server.close((error) => (error ? reject(error) : resolve()));
  });
}
