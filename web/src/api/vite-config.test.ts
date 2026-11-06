import { describe, expect, it } from 'vitest';
import config from '../../vite.config';

describe('Vite development API proxy', () => {
  it('forwards /api to the local backend without rewriting Host', () => {
    expect(config.server?.proxy).toMatchObject({
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: false,
      },
    });
  });
});
