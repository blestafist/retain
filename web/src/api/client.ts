const API_BASE = '/api/v1';

export interface User {
  id: number;
  sync_scope: number;
  email: string;
  name: string;
  is_admin?: boolean;
}

export interface UserSettings {
  new_per_day: number;
  max_reviews_per_day: number;
  request_retention: number;
  maximum_interval: number;
  learning_steps: string;
  relearning_steps: string;
  fsrs_weights: number[] | null;
  timezone: string;
  day_start_hour: number;
  reminder_time: string | null;
  tts_auto: boolean;
  theme_mode: 'light' | 'dark' | 'system';
  theme_seed: string;
  theme_variant: string;
  theme_contrast: number;
}

export interface MeResponse {
  user: User;
  settings: UserSettings;
}

export type UserSettingsPatch = Partial<UserSettings>;

export interface ApiErrorBody {
  error: {
    code: string;
    message: string;
  };
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly serverMessage?: string;

  constructor(status: number, code: string, serverMessage?: string) {
    super(serverMessage || `HTTP ${status}`);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.serverMessage = serverMessage;
  }
}

type RequestOptions = Omit<RequestInit, 'body' | 'credentials'> & {
  body?: unknown;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function parseBody(text: string): unknown {
  if (!text) return undefined;
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return text;
  }
}

function parseJson(text: string): { ok: true; value: unknown } | { ok: false } {
  try {
    return { ok: true, value: JSON.parse(text) as unknown };
  } catch {
    return { ok: false };
  }
}

function isJsonContentType(contentType: string | null): boolean {
  const mediaType = contentType?.split(';', 1)[0]?.trim().toLowerCase();
  return mediaType === 'application/json'
    || (mediaType?.startsWith('application/') === true && mediaType.endsWith('+json'));
}

function isUserSettings(value: unknown): value is UserSettings {
  if (!isRecord(value)) return false;

  return typeof value.new_per_day === 'number'
    && typeof value.max_reviews_per_day === 'number'
    && typeof value.request_retention === 'number'
    && typeof value.maximum_interval === 'number'
    && typeof value.learning_steps === 'string'
    && typeof value.relearning_steps === 'string'
    && (value.fsrs_weights === null
      || (Array.isArray(value.fsrs_weights) && value.fsrs_weights.every((weight) => typeof weight === 'number')))
    && typeof value.timezone === 'string'
    && typeof value.day_start_hour === 'number'
    && (typeof value.reminder_time === 'string' || value.reminder_time === null)
    && typeof value.tts_auto === 'boolean'
    && (value.theme_mode === 'light' || value.theme_mode === 'dark' || value.theme_mode === 'system')
    && typeof value.theme_seed === 'string'
    && typeof value.theme_variant === 'string'
    && typeof value.theme_contrast === 'number';
}

function isMeResponse(value: unknown): value is MeResponse {
  if (!isRecord(value) || !isRecord(value.user)) return false;

  const { user } = value;
  return typeof user.id === 'number'
    && typeof user.sync_scope === 'number'
    && Number.isSafeInteger(user.sync_scope)
    && user.sync_scope > 0
    && typeof user.email === 'string'
    && typeof user.name === 'string'
    && (user.is_admin === undefined || typeof user.is_admin === 'boolean')
    && isUserSettings(value.settings);
}

function invalidResponseError(status: number): ApiError {
  return new ApiError(status, 'invalid_response');
}

function errorFromResponse(status: number, statusText: string, body: unknown): ApiError {
  if (isRecord(body) && isRecord(body.error)) {
    const { code, message } = body.error;
    return new ApiError(
      status,
      typeof code === 'string' && code ? code : 'request_failed',
      typeof message === 'string' && message ? message : undefined,
    );
  }

  const serverMessage = typeof body === 'string' && body.trim() && !/<[a-z][\s\S]*>/i.test(body)
    ? body.trim()
    : undefined;
  return new ApiError(status, 'request_failed', serverMessage || statusText || undefined);
}

async function request<T>(
  path: string,
  options: RequestOptions = {},
  validateResponse?: (body: unknown) => body is T,
): Promise<T> {
  const headers = new Headers(options.headers);
  headers.set('Accept', 'application/json');
  if (options.body !== undefined) headers.set('Content-Type', 'application/json');

  const response = await fetch(`${API_BASE}${path}`, {
    ...options,
    credentials: 'same-origin',
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  });
  const text = await response.text();

  if (!response.ok) throw errorFromResponse(response.status, response.statusText, parseBody(text));
  if (response.status === 204) {
    if (validateResponse && !validateResponse(undefined)) throw invalidResponseError(response.status);
    return undefined as T;
  }
  if (!text) {
    if (validateResponse && !validateResponse(undefined)) throw invalidResponseError(response.status);
    return undefined as T;
  }
  if (!isJsonContentType(response.headers.get('content-type'))) {
    throw invalidResponseError(response.status);
  }

  const parsed = parseJson(text);
  if (!parsed.ok || (validateResponse && !validateResponse(parsed.value))) {
    throw invalidResponseError(response.status);
  }
  return parsed.value as T;
}

export interface RegisterRequest {
  invite: string;
  email: string;
  name: string;
  password: string;
}

export interface LoginRequest {
  email: string;
  password: string;
}

export function register(input: RegisterRequest): Promise<void> {
  return request('/auth/register', { method: 'POST', body: input });
}

export function login(input: LoginRequest): Promise<void> {
  return request('/auth/login', { method: 'POST', body: input });
}

export function logout(): Promise<void> {
  return request('/auth/logout', { method: 'POST' });
}

export function getMe(): Promise<MeResponse> {
  return request('/me', {}, isMeResponse);
}

export function patchMeSettings(settings: UserSettingsPatch): Promise<UserSettings> {
  return request('/me/settings', { method: 'PATCH', body: settings });
}
