import { ApiError } from '../api/client';

export interface AdminLlmModels {
  models: string[];
  selected_model: string;
  available: boolean;
  warning: string | null;
}

export interface AdminLlmUsageModel {
  model: string;
  kind: string;
  requests: number;
  cached_requests: number;
  prompt_tokens: number;
  completion_tokens: number;
}

export interface AdminLlmUsageUser {
  user_id: number;
  name: string;
  email: string;
  models: AdminLlmUsageModel[];
}

export interface AdminLlmUsage {
  users: AdminLlmUsageUser[];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isNonNegativeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}

function invalidResponse(status = 200): ApiError {
  return new ApiError(status, 'invalid_response');
}

function decodeModels(value: unknown, status = 200): AdminLlmModels {
  if (!isRecord(value)
    || !Array.isArray(value.models)
    || !value.models.every((model) => typeof model === 'string' && model.trim() !== '')
    || typeof value.selected_model !== 'string'
    || typeof value.available !== 'boolean'
    || (value.warning !== null && typeof value.warning !== 'string')) {
    throw invalidResponse(status);
  }
  return {
    models: value.models as string[],
    selected_model: value.selected_model,
    available: value.available,
    warning: value.warning as string | null,
  };
}

function decodeUsage(value: unknown, status = 200): AdminLlmUsage {
  if (!isRecord(value) || !Array.isArray(value.users)) throw invalidResponse(status);
  const users = value.users.map((user) => {
    if (!isRecord(user)
      || !isNonNegativeInteger(user.user_id)
      || typeof user.name !== 'string'
      || typeof user.email !== 'string'
      || !Array.isArray(user.models)) {
      throw invalidResponse(status);
    }
    const models = user.models.map((usage) => {
      if (!isRecord(usage)
        || typeof usage.model !== 'string'
        || typeof usage.kind !== 'string'
        || !isNonNegativeInteger(usage.requests)
        || !isNonNegativeInteger(usage.cached_requests)
        || !isNonNegativeInteger(usage.prompt_tokens)
        || !isNonNegativeInteger(usage.completion_tokens)) {
        throw invalidResponse(status);
      }
      return {
        model: usage.model,
        kind: usage.kind,
        requests: usage.requests,
        cached_requests: usage.cached_requests,
        prompt_tokens: usage.prompt_tokens,
        completion_tokens: usage.completion_tokens,
      };
    });
    return { user_id: user.user_id, name: user.name, email: user.email, models };
  });
  return { users };
}

type AdminLlmRoute = '/api/v1/admin/llm/models' | '/api/v1/admin/llm/model' | '/api/v1/admin/llm-usage';

async function request(url: AdminLlmRoute, options: { method?: string; body?: unknown } = {}): Promise<unknown> {
  const headers = new Headers({ Accept: 'application/json' });
  if (options.body !== undefined) headers.set('Content-Type', 'application/json');
  const response = await fetch(url, {
    method: options.method,
    credentials: 'same-origin',
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  });
  const text = await response.text();
  let body: unknown;
  if (text) {
    try {
      body = JSON.parse(text) as unknown;
    } catch {
      body = undefined;
    }
  }
  if (!response.ok) {
    const error = isRecord(body) && isRecord(body.error) ? body.error : undefined;
    throw new ApiError(
      response.status,
      typeof error?.code === 'string' ? error.code : 'request_failed',
      typeof error?.message === 'string' ? error.message : response.statusText || undefined,
    );
  }
  if (body === undefined) throw invalidResponse(response.status);
  return body;
}

export async function getAdminLlmModels(): Promise<AdminLlmModels> {
  return decodeModels(await request('/api/v1/admin/llm/models'));
}

export async function setAdminLlmModel(model: string): Promise<{ selected_model: string }> {
  const value = await request('/api/v1/admin/llm/model', { method: 'PUT', body: { model } });
  if (!isRecord(value) || typeof value.selected_model !== 'string') throw invalidResponse();
  return { selected_model: value.selected_model };
}

export async function getAdminLlmUsage(): Promise<AdminLlmUsage> {
  return decodeUsage(await request('/api/v1/admin/llm-usage'));
}

export { decodeModels as decodeAdminLlmModels, decodeUsage as decodeAdminLlmUsage };
