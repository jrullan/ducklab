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
  /** The directory is gone. Selecting one of these silently produced an empty
   * board that read as "no tasks" rather than "this project is not there". */
  missing?: boolean;
}

/** A gate that was actually run. Mirrors service.GateResult. */
export type GateResult = {
  gate: string;
  command: string;
  exit_code: number;
  output: string;
  duration_s: number;
  green: boolean;
};

/** A project's gate, and what could be. Mirrors service.GateStatus. */
export type GateStatus = {
  mode: string;
  command: string;
  detected: string;
  detected_command?: string;
  /** Detection found something the project is not using — the only case worth
   * acting on. */
  adoptable: boolean;
  /** What runs currently produce at best. Spelled out because "none" does not
   * obviously mean "nothing can ever pass". */
  best_verdict: string;
};

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
  /** The run that produced this document, so a client can fetch what it was
   * asked for. */
  run_id?: string;
  /** Present only while a stage's output is awaiting the human gate. */
  proposal?: {
    diff: string;
    /** The proposed document itself. A first draft has nothing to diff
     * against, so this is what a person actually reads to decide. */
    markdown?: string;
    sections?: Section[] | null;
    run_id?: string;
    ducklings?: string[];
  };
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

/** One role and the duckling that will play it. Mirrors service.RosterEntry. */
export type RosterEntry = {
  role: string;
  duckling: string;
  /** "project" when project.toml declares it, "default" when the engine chose.
   * A person needs to know which assignments are theirs. */
  source: string;
};

/** A configured endpoint. Mirrors service.ProviderView.
 *
 * There is no key field and there never will be: a provider records the name
 * of an environment variable, and the engine reads the value at call time. */
export type ProviderView = {
  id: string;
  kind: string;
  base_url: string;
  api_key_env?: string;
  /** Whether that variable is set in the engine's environment. */
  key_present: boolean;
  /** Ducklings that would break if this provider went away. */
  in_use?: string[];
};

/** One past bench run. Mirrors service.BenchSummary. */
export type BenchSummary = {
  suite: string;
  suite_version: number;
  started_at: string;
  stamp: string;
  cells: number;
  passed: number;
  errors: number;
};

/** One (task, duckling, mode) measurement. Mirrors bench.Cell. */
export type BenchCell = {
  task: string;
  duckling: string;
  mode: string;
  run_id: string;
  verdict: string;
  tokens: number;
  estimated: boolean;
  cost_usd: number;
  wallclock_ms: number;
  /** Set when the harness could not run the cell, which is not the same as a
   * task the model failed. */
  error?: string;
};

/** A whole bench invocation. Mirrors bench.Result. */
export type BenchResult = {
  suite: string;
  suite_version: number;
  started_at: string;
  ducklings: string[];
  modes: string[];
  cells: BenchCell[];
};

/** One aggregated group in a report. Mirrors report.Row. */
export type ReportRow = {
  key: string;
  runs: number;
  passed: number;
  unverified: number;
  failed: number;
  tokens: number;
  cost_usd: number;
  wallclock_ms: number;
  /** True when any run in the group had token counts by estimate. Never
   * summed with measured counts without saying so (04 §7). */
  estimated: boolean;
};

/** One mode measured against the solo baseline. Mirrors report.Delta. */
export type ReportDelta = {
  key: string;
  pass_rate: number;
  points_vs_baseline: number;
  n: number;
};

/** One report in the operate loop. Mirrors bug.Bug. */
export interface Bug {
  id: string;
  title: string;
  body?: string;
  severity: string;
  status: string;
  duplicate_of?: string;
  task_id?: string;
  source: string;
  reporter?: string;
  created_at: string;
  updated_at: string;
}

/** One filed review, enough to list without reading the body. */
export interface ReviewSummary {
  task_id: string;
  verdict: string;
  findings: number;
  commit?: string;
  mode?: string;
  reviewed_at?: string;
}

export interface ReleaseSummary {
  version: string;
  since?: string;
  tasks: number;
  unverified?: number;
  /** Still awaiting a person. A draft and a cut release are not the same
   * claim, and showing them alike would let an unapproved one read as
   * shipped. */
  drafted: boolean;
  tagged: boolean;
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
  /** Create or adopt a project at a path. A folder that is already a project
   * is opened rather than refused, so pointing at an existing one is not a
   * mistake the person has to undo. */
  projectInit(path: string, name: string, gitInit: boolean) {
    return this.request<Project>("POST", "/v1/projects", {
      path,
      name,
      git_init: gitInit,
    });
  }
  /** Change what the engine records about a project. Keys are the same ones
   * `ducklab project set` takes. */
  projectUpdate(id: string, keys: Record<string, string>) {
    return this.request<Project>("PATCH", `/v1/projects/${id}`, keys);
  }
  /** Start a build run. Returns immediately; the work is watched on the run. */
  runStart(
    projectId: string,
    taskId: string,
    opts: { mode?: string; ducklings?: string[]; rounds?: number; yes?: boolean } = {},
  ) {
    return this.request<Run>("POST", `/v1/projects/${projectId}/runs`, {
      task_id: taskId,
      mode: opts.mode || "solo",
      ducklings: opts.ducklings ?? [],
      rounds: opts.rounds ?? 0,
      autonomy: opts.yes ? "yolo" : "",
    });
  }

  /** Write the failing test for a task, before the code exists. */
  testStart(projectId: string, taskId: string, duckling = "") {
    return this.request<Run>("POST", `/v1/projects/${projectId}/tests`, {
      task_id: taskId,
      duckling,
    });
  }

  /** Review an accepted task. */
  reviewStart(projectId: string, taskId: string) {
    return this.request<Run>("POST", `/v1/projects/${projectId}/reviews`, { task_id: taskId });
  }

  /** The roster as it will actually be used — including the roles the engine
   * filled in, which the file does not declare. */
  roster(projectId: string) {
    return this.request<{ entries: RosterEntry[] | null; warning?: string }>(
      "GET",
      `/v1/projects/${projectId}/roster`,
    ).then((r) => ({ entries: r.entries ?? [], warning: r.warning }));
  }
  rosterSet(projectId: string, role: string, duckling: string) {
    return this.request<unknown>("PUT", `/v1/projects/${projectId}/roster`, { role, duckling });
  }

  /** Ask a duckling to say something, to find out whether it answers at all. */
  ducklingProbe(id: string) {
    return this.request<Record<string, unknown>>("POST", `/v1/ducklings/${id}/probe`);
  }

  /** The configured gate beside the one detection finds today. */
  projectGate(id: string) {
    return this.request<GateStatus>("GET", `/v1/projects/${id}/gate`);
  }
  /** Run the gate now. On demand, never on a page load: a gate can be a whole
   * test suite, and a screen that ran one every time it opened would make
   * looking expensive — which is how people stop looking. */
  gateRun(id: string) {
    return this.request<GateResult>("POST", `/v1/projects/${id}/gate/run`);
  }
  /** Adopt the detected gate. Never automatic: a gate decides what a verdict
   * means, and changing that silently makes two runs incomparable while both
   * claim to have been measured the same way. */
  projectGateAdopt(id: string) {
    return this.request<GateStatus>("POST", `/v1/projects/${id}/gate`);
  }
  /** Forget a project. The engine unregisters it; the files stay. */
  projectForget(id: string) {
    return this.request<unknown>("DELETE", `/v1/projects/${id}`);
  }
  projectStatus(id: string) {
    return this.request<Record<string, unknown>>("GET", `/v1/projects/${id}/status`);
  }
  /** Configured providers. Carries the *name* of the key's environment
   * variable and whether it is set — never a key (I10). */
  providers() {
    return this.request<{ items: ProviderView[] | null }>("GET", "/v1/providers").then(
      (r) => r.items ?? [],
    );
  }
  providerSet(id: string, body: Partial<ProviderView>) {
    return this.request<unknown>("PUT", `/v1/providers/${id}`, body);
  }
  providerRemove(id: string) {
    return this.request<unknown>("DELETE", `/v1/providers/${id}`);
  }
  ducklingSet(id: string, body: Record<string, unknown>) {
    return this.request<unknown>("PUT", `/v1/ducklings/${id}`, body);
  }
  ducklingRemove(id: string) {
    return this.request<unknown>("DELETE", `/v1/ducklings/${id}`);
  }
  ducklingTest(id: string, prompt: string) {
    return this.request<{ text: string; prompt_tokens?: number; completion_tokens?: number; cost_usd?: number }>(
      "POST",
      `/v1/ducklings/${id}/test`,
      { prompt },
    );
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
    return this.request<{ diff: string; tests?: string }>("GET", `/v1/runs/${id}/diff`).then((r) => ({
      diff: r.diff ?? "",
      // Present only for a run flagged for editing tests it was not asked to
      // touch (05 §5.3).
      tests: r.tests ?? "",
    }));
  }
  runCandidates(id: string) {
    return this.request<{ items: Candidate[] }>("GET", `/v1/runs/${id}/candidates`).then(
      (r) => r.items ?? [],
    );
  }
  /** What a person asked this run for. Empty for runs that were not seeded. */
  runBrief(id: string) {
    return this.request<{ brief: string }>("GET", `/v1/runs/${id}/brief`).then((r) => r.brief ?? "");
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
  /** Start intake, spec or plan.
   *
   * `from` seeds intake: a file path if it reads as one, otherwise the brief
   * text itself — the engine treats an unreadable path as the brief, so
   * pasting a sentence needs no file. */
  stageStart(
    projectId: string,
    stage: string,
    opts: { from?: string; mode?: string; revise?: string; rounds?: number } = {},
  ) {
    return this.request<Run>("POST", `/v1/projects/${projectId}/stages/${stage}`, {
      stage,
      from: opts.from ?? "",
      // Set only when revising: it turns the run from "write this document"
      // into "edit this one", which is the answer to a draft that is almost
      // right.
      revise: opts.revise ?? "",
      mode: opts.mode ?? "",
      rounds: opts.rounds ?? 0,
      stream: true,
    });
  }

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
  bugs(projectId: string, openOnly = false) {
    const q = openOnly ? "?open=true" : "";
    return this.request<{ items: Bug[] | null; total: number }>(
      "GET",
      `/v1/projects/${projectId}/bugs${q}`,
    ).then((r) => r.items ?? []);
  }
  /** Move a bug. The engine refuses transitions the loop does not allow, so
   * the error it returns is the one worth showing. */
  moveBug(projectId: string, bugId: string, status: string) {
    return this.request<Bug>("POST", `/v1/projects/${projectId}/bugs/${bugId}/status`, {
      status,
    });
  }
  promoteBug(projectId: string, bugId: string) {
    return this.request<{ bug: string; task: string; status: string }>(
      "POST",
      `/v1/projects/${projectId}/bugs/${bugId}/promote`,
    );
  }
  /** The solo-baseline comparison (03 §3.10). `rendered` is the engine's own
   * table; the rows are for charting. */
  report(projectId: string, by: "mode" | "duckling" | "role" | "task", since = "") {
    const q = new URLSearchParams({ by });
    if (since) q.set("since", since);
    return this.request<{
      by: string;
      baseline: string;
      rows: ReportRow[] | null;
      deltas: ReportDelta[] | null;
      rendered: string;
    }>("GET", `/v1/projects/${projectId}/report?${q}`).then((r) => ({
      ...r,
      rows: r.rows ?? [],
      deltas: r.deltas ?? [],
    }));
  }

  /** Past bench results, newest first. Not project-scoped: a bench measures
   * the models, not a repo. */
  benchList() {
    return this.request<{ items: BenchSummary[] | null }>("GET", "/v1/bench").then(
      (r) => r.items ?? [],
    );
  }

  benchGet(suite: string, stamp: string) {
    return this.request<{ result: BenchResult; rendered: string }>(
      "GET",
      `/v1/bench/${suite}/${stamp}`,
    );
  }

  reviews(projectId: string) {
    return this.request<{ items: ReviewSummary[] | null }>(
      "GET",
      `/v1/projects/${projectId}/reviews`,
    ).then((r) => r.items ?? []);
  }
  review(projectId: string, taskId: string) {
    return this.request<{ markdown: string }>(
      "GET",
      `/v1/projects/${projectId}/reviews/${taskId}`,
    ).then((r) => r.markdown ?? "");
  }
  releases(projectId: string) {
    return this.request<{ items: ReleaseSummary[] | null }>(
      "GET",
      `/v1/projects/${projectId}/releases`,
    ).then((r) => r.items ?? []);
  }
  release(projectId: string, version: string) {
    return this.request<{ markdown: string }>(
      "GET",
      `/v1/projects/${projectId}/releases/${version}`,
    ).then((r) => r.markdown ?? "");
  }
  tasks(projectId: string) {
    // items is null, not [], when a project has no plan yet.
    return this.request<{ items: Task[] | null; total: number }>(
      "GET",
      `/v1/projects/${projectId}/tasks`,
    ).then((r) => r.items ?? []);
  }
}
