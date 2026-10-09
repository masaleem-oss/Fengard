// every write sends the X-Fengard header for csrf

export class ApiError extends Error {
  constructor(message, status) {
    super(message);
    this.status = status;
  }
}

let onUnauthorized = () => {};
export const setUnauthorizedHandler = (fn) => (onUnauthorized = fn);

export async function api(path, { method = 'GET', body, raw } = {}) {
  const headers = { 'X-Fengard': '1' };
  let payload;
  if (raw !== undefined) {
    payload = raw;
    headers['Content-Type'] = 'application/json';
  } else if (body !== undefined) {
    payload = JSON.stringify(body);
    headers['Content-Type'] = 'application/json';
  }
  let res;
  try {
    res = await fetch(path, { method, headers, body: payload, credentials: 'same-origin' });
  } catch {
    throw new ApiError("Can't reach Fengard. Check your connection.", 0);
  }
  const isJSON = res.headers.get('Content-Type')?.includes('json');
  const data = isJSON ? await res.json().catch(() => ({})) : await res.text();
  if (res.status === 401 && !path.startsWith('/api/login') && !path.startsWith('/api/session')) {
    onUnauthorized();
  }
  if (!res.ok) throw new ApiError((isJSON && data.error) || res.statusText || 'Request failed', res.status);
  return data;
}

export const get = (p) => api(p);
export const post = (p, body = {}) => api(p, { method: 'POST', body });
export const put = (p, body) => api(p, { method: 'PUT', body });
export const patch = (p, body) => api(p, { method: 'PATCH', body });
export const del = (p, body) => api(p, { method: 'DELETE', body });
