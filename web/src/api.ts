export type Job = {
  id: number;
  url: string;
  kind: string;
  name: string;
  status: "queued" | "resolving" | "running" | "done" | "failed" | "cancelled";
  error?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
  total: number;
  counts: Record<string, number>;
};

export type Track = {
  pos: number;
  title: string;
  artists: string[];
  album?: string;
  isrc?: string;
  status: string;
  path?: string;
  bytes?: number;
  reason?: string;
};

export type JobDetail = Job & { tracks: Track[] };

export type Hit = { isrc: string; title: string; artists: string[]; duration_ms: number };

export class Unauthorized extends Error {}

const keyName = "dredge-api-key";

export const apiKey = {
  get: () => localStorage.getItem(keyName) ?? "",
  set: (k: string) => localStorage.setItem(keyName, k),
};

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  const key = apiKey.get();
  if (key) headers["X-Api-Key"] = key;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const r = await fetch("/api" + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (r.status === 401) throw new Unauthorized("API key needed");
  if (r.status === 204) return undefined as T;
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(data.error ?? `${r.status} ${r.statusText}`);
  return data as T;
}

export const api = {
  list: () => call<Job[]>("GET", "/jobs"),
  get: (id: number) => call<JobDetail>("GET", `/jobs/${id}`),
  submit: (url: string) => call<Job>("POST", "/jobs", { url }),
  search: (q: string) => call<Hit[]>("GET", `/search?q=${encodeURIComponent(q)}`),
  submitTrack: (h: Hit) =>
    call<Job>("POST", "/jobs", { isrc: h.isrc, title: h.title, artists: h.artists }),
  retry: (id: number) => call<Job>("POST", `/jobs/${id}/retry`),
  cancel: (id: number) => call<Job>("POST", `/jobs/${id}/cancel`),
  remove: (id: number) => call<void>("DELETE", `/jobs/${id}`),
};

export const active = (j: { status: string }) =>
  j.status === "queued" || j.status === "resolving" || j.status === "running";

export const isLink = (s: string) => /^(https?:\/\/|spotify:)/i.test(s.trim());

export function clock(ms: number): string {
  const s = Math.floor(ms / 1000);
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

export function size(n = 0): string {
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + " MB";
  if (n >= 1 << 10) return (n / (1 << 10)).toFixed(1) + " KB";
  return n + " B";
}

export function ago(iso?: string): string {
  if (!iso) return "";
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return Math.floor(s / 60) + " min ago";
  if (s < 86400) return Math.floor(s / 3600) + " h ago";
  return new Date(iso).toLocaleDateString();
}
