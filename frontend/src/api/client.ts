/**
 * Typed client for the engine API.
 *
 * Hand-written for v0.3; `make api` will regenerate it from the engine's
 * OpenAPI document, at which point this file becomes generated and must not
 * be hand-edited (07 §7.3). The shapes below mirror the engine's DTOs exactly
 * so that swap is mechanical.
 */

export interface Project {
  id: string;
  path: string;
  name: string;
  gate?: string;
  autonomy?: string;
}

export interface Run {
  id: string;
  project_id: string;
  stage: string;
  mode: string;
  task_id: string;
  status: "running" | "queued" | "paused" | "done" | "failed";
  verdict: string;
  accepted?: boolean;
  commit_sha?: string;
  started_at: string;
  ended_at?: string;
  roster?: Record<string, string>;
  pending_kind?: string;
  pending_since?: string;
  pending_data?: Record<string, unknown>;
  resolution?: string;
  warning?: string;
  budget?: { usd: number; tokens: number; turns: number; wallclock_s: number };
}

export interface Duckling {
  id: string;
  provider: string;
  model: string;
  roles?: string[];
  caps?: { native_tools: boolean; json_mode?: boolean; context_tokens: number };
  cost?: { input_per_mtok: number; output_per_mtok: number };
}

/** One numbered item in an artifact: a REQ, a SPEC, a milestone. */
export interface Section {
  id: string;
  title: string;
  body: string;
  implements?: string[];
  fields?: Record<string, string>;
  children?: Section[];
}

export interface Artifact {
  kind: string;
  version: number;
  approved: boolean;
  markdown: string;
  sections: Section[] | null;
  /** Present only while a stage's output is awaiting the human gate. */
  proposal?: { diff: string; run_id?: string; ducklings?: string[] };
}

/** A break in the traceability spine. Produced deterministically, never by a
 * model, so the UI can state these as fact rather than opinion. */
export interface TraceError {
  kind: string;
  id: string;
  detail: string;
}

/** Mirrors service.TaskView. Status is derived from run records, never stored
 * on the task — a model rewriting the plan must not be able to mark its own
 * work accepted. That is why the board has no drag-to-move. */
export interface Task {
  id: string;
  title: string;
  milestone: string;
  status: string;
  implements?: string[];
  complexity?: string;
  depends_on?: string[];
  body?: string;
}

/** A tournament candidate. There is no author field, by design (I7). */
export interface Candidate {
  label: string;
  diff: string;
  gate?: string;
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code?: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export interface ClientOptions {
  baseUrl: string;
  token: string;
  version?: string;
  fetchFn?: typeof fetch;
}

export class EngineClient {
  constructor(private opts: ClientOptions) {}

  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const f = this.opts.fetchFn ?? fetch;
    const headers: Record<string, string> = {
      Authorization: `Bearer ${this.opts.token}`,
    };
    if (this.opts.version) headers["X-Ducklab-Client"] = this.opts.version;
    if (body !== undefined) headers["Content-Type"] = "application/json";

    const res = await f(`${this.opts.baseUrl}${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });

    if (res.status === 204) return undefined as T;
    const text = await res.text();
    let parsed: unknown = undefined;
    if (text) {
      try {
        parsed = JSON.parse(text);
      } catch {
        parsed = undefined;
      }
    }

    if (!res.ok) {
      const err = (parsed as { error?: { code?: string; message?: string } })?.error;
      throw new ApiError(err?.message ?? `${method} ${path} failed`, res.status, err?.code);
    }
    return parsed as T;
  }

  health() {
    return this.request<{ ok: boolean; version: string; active_runs: number }>("GET", "/v1/health");
  }
  projects() {
    return this.request<{ items: Project[] }>("GET", "/v1/projects").then((r) => r.items ?? []);
  }
  projectStatus(id: string) {
    return this.request<Record<string, unknown>>("GET", `/v1/projects/${id}/status`);
  }
  ducklings() {
    return this.request<{ items: Duckling[] }>("GET", "/v1/ducklings").then((r) => r.items ?? []);
  }
  runs(projectId?: string) {
    const q = projectId ? `?project=${encodeURIComponent(projectId)}` : "";
    return this.request<{ items: Run[] }>("GET", `/v1/runs${q}`).then((r) => r.items ?? []);
  }
  run(id: string) {
    return this.request<{ run: Run; events: unknown[] }>("GET", `/v1/runs/${id}`);
  }
  runDiff(id: string) {
    return this.request<{ diff: string }>("GET", `/v1/runs/${id}/diff`).then((r) => r.diff ?? "");
  }
  runCandidates(id: string) {
    return this.request<{ items: Candidate[] }>("GET", `/v1/runs/${id}/candidates`).then(
      (r) => r.items ?? [],
    );
  }
  runVerify(id: string, tail = 500) {
    return this.request<{ output: string }>("GET", `/v1/runs/${id}/verify?tail=${tail}`).then(
      (r) => r.output ?? "",
    );
  }
  accept(id: string, message = "") {
    return this.request<{ commit_sha: string }>("POST", `/v1/runs/${id}/accept`, { message });
  }
  reject(id: string, reason = "") {
    return this.request<void>("POST", `/v1/runs/${id}/reject`, { reason });
  }
  abort(id: string) {
    return this.request<void>("POST", `/v1/runs/${id}/abort`);
  }
  answer(id: string, questionId: string, answer: string) {
    return this.request<void>("POST", `/v1/runs/${id}/answer`, {
      question_id: questionId,
      answer,
    });
  }

  artifact(projectId: string, kind: string) {
    return this.request<Artifact>("GET", `/v1/projects/${projectId}/artifacts/${kind}`);
  }
  /** Promote a proposal to the artifact. This is the human gate (05 §1.1) —
   * the only caller is a person clicking Accept, never a model. */
  promote(projectId: string, kind: string, approvedBy = "human") {
    return this.request<Artifact>("POST", `/v1/projects/${projectId}/artifacts/${kind}/promote`, {
      approved_by: approvedBy,
    });
  }
  traceCheck(projectId: string) {
    return this.request<{ errors: TraceError[] | null }>(
      "GET",
      `/v1/projects/${projectId}/trace/check`,
    ).then((r) => r.errors ?? []);
  }
  traceShow(projectId: string, id: string) {
    return this.request<Record<string, unknown>>("GET", `/v1/projects/${projectId}/trace/${id}`);
  }
  tasks(projectId: string) {
    // items is null, not [], when a project has no plan yet.
    return this.request<{ items: Task[] | null; total: number }>(
      "GET",
      `/v1/projects/${projectId}/tasks`,
    ).then((r) => r.items ?? []);
  }
}
