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
  /** The tree holds committed files beyond .ducklab: a codebase to adopt,
   * not an idea to interview. Decides the Cycle empty state's doors. */
  has_code?: boolean;
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

/** Mirrors service.AppStatus: the running system as a first-class object. */
export interface AppStatus {
  configured: boolean;
  command?: string;
  url?: string;
  running: boolean;
  pid?: number;
  started_at?: string;
  health?: string;
  preflight?: string;
  requires?: string;
  exit_error?: string;
  log_tail?: string;
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
  /** Set when an accepted test-first's commit was later retired (reverted). */
  revert_sha?: string;
  /** The run finished without touching a file: the work was already in the
   * tree. Wears FAILED in the metrics on purpose; the UI says this instead. */
  no_changes?: boolean;
  started_at: string;
  ended_at?: string;
  roster?: Record<string, string>;
  pending_kind?: string;
  pending_since?: string;
  pending_data?: Record<string, unknown>;
  resolution?: string;
  warning?: string;
  /** Why the run failed, in the engine's words. Some of these are written to be
   * acted on — split names the file two subtasks both claimed. */
  failure?: string;
  /** The actions a person may legally take on this run, in the order to offer
   * them. Stated by the engine; clients render buttons from this list and never
   * encode the loop's rules themselves (docs/ux-evaluation.md §5.4). */
  next?: string[];
  /** True when any call's token count was estimated rather than reported by
   * the provider: the run's cost is then an estimate too, and every view
   * marks it ~ (04 §7). */
  tokens_estimated?: boolean;
  /** The run's own cap on model calls per reply: absent/0 the configured
   * defaults, positive a per-run override, -1 lifted (at launch or live). */
  agent_turns?: number;
  /** A test-first's pre-authorized build, straight off the record — what a
   * relaunch must carry so the chain survives the retry. */
  chain_build?: { mode?: string; ducklings?: string[]; agent_turns?: number; budget?: { max_tokens?: number } };
  /** Per-duckling spend, attributed as each call lands. Served live for an
   * active run, so a view opened mid-run starts from the truth instead of
   * zeros. */
  spend?: Record<string, { calls: number; tokens: number; cost_usd: number; estimated?: boolean }>;
  budget?: {
    usd: number;
    tokens: number;
    turns: number;
    wallclock_s: number;
    /** What this run was actually given. The meter used to hardcode 400000, so a
     * run started with a raised ceiling was drawn against a limit it did not
     * have. */
    limit?: { usd: number; tokens: number; turns: number; wallclock_s: number };
  };
}

/** How the model is asked to generate. The engine has always accepted these;
 * the desktop form sent none of them, so max_tokens and disable_thinking were
 * reachable only by hand-editing config.toml — and because the form posted an
 * empty `params`, editing a duckling in the UI wiped whatever was there. */
export interface SamplingParams {
  temperature?: number | null;
  top_p?: number | null;
  max_tokens?: number | null;
  disable_thinking?: boolean;
  stop?: string[] | null;
}

/** The default run budget. Mirrors service.BudgetView.
 *
 * max_tokens counts prompt AND completion, every round. Each model call
 * re-sends the whole conversation, so the same context is counted again on each
 * one — a run can spend most of its budget on input without writing much. */
export interface BudgetView {
  max_usd: number;
  max_tokens: number;
  max_turns: number;
  max_wallclock_s: number;
}

/** Mirrors service.ModeDefaultsView.
 *
 * Two different limits. `rounds` bounds the conversation — how many times a
 * reviewer gets to push back. `agent_max_turns` bounds ONE participant's turn:
 * the model calling tools, reading results, calling again. A run whose
 * implementer works in circles is stopped by the second, not the first. */
export interface ModeDefaultsView {
  rounds: Record<string, number>;
  agent_max_turns: number;
  /** What each mode does when nothing overrides it, so a client can show the
   * real number instead of an empty box. */
  script_rounds?: Record<string, number>;
  /** The duckling line-up to use for each mode when a run names none, in order.
   * A combination that works is a finding, and re-ticking the same boxes on
   * every run is how a finding gets lost. */
  ducklings?: Record<string, string[]>;
  /** How many model calls one turn of a role may chain. A reviewer gets fewer
   * than an implementer on purpose: reviewing is reading and giving a verdict,
   * not iterating. */
  role_turns?: Record<string, number>;
  script_role_turns?: Record<string, number>;
  /** How many ducklings each mode can seat — zero or absent means as many as
   * are ticked. Rendered as disabled checkboxes, so a full mode says "no more
   * chairs" at the box instead of failing at Save. */
  seats?: Record<string, number>;
  /** The modes launchers open on: the person who always builds in pair and
   * tests in solo should not re-pick both on every task. */
  build_mode?: string;
  test_mode?: string;
}

export interface Duckling {
  id: string;
  provider: string;
  model: string;
  roles?: string[];
  notes?: string;
  params?: SamplingParams;
  /** Which of the eight series slots to draw this duckling in, or absent to let
   * the fleet order decide. A slot number and never a hex, so the palette keeps
   * its light and dark variants. */
  color?: number;
  caps?: { native_tools: boolean; json_mode?: boolean; context_tokens: number; vision?: boolean };
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
/** One step of the project guide: what to do, why it is next, where to act.
 * The action names the outcome first and the harness term second — new users
 * do not share the vocabulary yet. */
export interface NextStep {
  id: string;
  action: string;
  reason: string;
  kind: "run" | "task" | "bug" | "stage" | "project";
  ref?: string;
}

export interface Task {
  id: string;
  title: string;
  milestone: string;
  status: string;
  implements?: string[];
  complexity?: string;
  depends_on?: string[];
  body?: string;
  /** Why the work stopped, when status is "blocked". */
  blocked?: string;
  /** A committed failing test already defines done: the natural next act is
   * the build that makes it pass. */
  test_ready?: boolean;
  /** The actions a person may legally start from this task, stated by the
   * engine — run, test_first, review, remove. */
  next?: string[];
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
  /** The statuses this bug may legally move to, as the engine computes them.
   * Not worked out here: the loop's rules live in the engine, and a UI that
   * hardcoded them would drift the first time one changed — or leave a bug in a
   * state it happens not to handle with nothing to click on it. */
  next?: string[];  /** Attached files, screenshots mostly, by name. */
  attachments?: string[];
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

/** One model call, as recorded in llm.jsonl. */
export interface LLMCall {
  seq: number;
  ts: string;
  duckling: string;
  provider: string;
  /** The pool member OpenRouter routed this call to, when it says. */
  upstream?: string;
  model: string;
  role: string;
  request?: Record<string, unknown>;
  response?: Record<string, unknown>;
  usage?: Record<string, number>;
  cost_usd: number;
  latency_ms: number;
  estimated?: boolean;
  finish_reason?: string;
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

/** How a connection went stale: "older" — the engine predates this app and
 * must be restarted; "restarted" — the engine was replaced outside the app
 * and this window only needs to reconnect. */
export type StaleKind = "older" | "restarted";

export interface ClientOptions {
  baseUrl: string;
  token: string;
  version?: string;
  fetchFn?: typeof fetch;
  /** Called when a response reveals a stale connection — the signal the
   * banner listens for, with which remedy applies. The error still throws;
   * this is how the shell learns without every call site reporting upward. */
  onStale?: (kind: StaleKind) => void;
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
      // The engine answers unknown routes with the mux's plain-text 404, not
      // its JSON error shape — which is exactly what an engine OLDER THAN THIS
      // APP does for a route added since it started. The generic fallback
      // ("POST /v1/bench/start failed") reported that state without naming it,
      // and the one action that fixes it — restart the engine — went unsaid.
      if (err?.message === undefined && (res.status === 404 || res.status === 405)) {
        this.opts.onStale?.("older");
        throw new ApiError(
          `the engine does not know ${method} ${path} — it is older than this app. Restart the engine.`,
          res.status,
        );
      }
      // The inverse: the engine was restarted OUTSIDE the app, this window's
      // token died with the old process, and every request 401s. Twice this
      // month that state wore fifteen Load Errors and no explanation — the
      // classifier only knew the older-engine case. Same banner, other
      // remedy: reconnect (re-read the daemon's connection file), no restart
      // needed.
      if (res.status === 401) {
        this.opts.onStale?.("restarted");
        throw new ApiError(
          "the engine no longer recognizes this window's session — it was restarted outside the app. Reconnect.",
          res.status,
        );
      }
      throw new ApiError(
        err?.message ?? `${method} ${path} failed (${res.status}${text ? `: ${text.slice(0, 120)}` : ""})`,
        res.status,
        err?.code,
      );
    }
    return parsed as T;
  }

  health() {
    return this.request<{
      ok: boolean;
      version: string;
      /** The run queue's live counters — what explains a run sitting queued. */
      queue?: { running: number; waiting: number; limit: number };
    }>("GET", "/v1/health");
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
    opts: {
      mode?: string;
      ducklings?: string[];
      rounds?: number;
      yes?: boolean;
      /** Raise this one run's token ceiling above the configured default,
       * without changing the default for everything else. */
      maxTokens?: number;
      /** A run-specific instruction from the human, riding the prompt — the
       * channel for "address the previous reviewer's findings". */
      note?: string;
      /** Override how many model calls one reply may chain, for every role in
       * this run. The default cap exists to stop circling; a hard task can
       * need more looking. */
      agentTurns?: number;
    } = {},
  ) {
    return this.request<Run>("POST", `/v1/projects/${projectId}/runs`, {
      task_id: taskId,
      mode: opts.mode || "solo",
      ducklings: opts.ducklings ?? [],
      rounds: opts.rounds ?? 0,
      note: opts.note || undefined,
      agent_turns: opts.agentTurns || undefined,
      autonomy: opts.yes ? "yolo" : "",
      // Omitted rather than zeroed: the engine fills every unset limit from the
      // defaults, and a zero would be a ceiling of zero.
      ...(opts.maxTokens ? { budget: { max_tokens: opts.maxTokens } } : {}),
    });
  }

  /** Write the failing test for a task, before the code exists.
   *
   * thenBuild chains the person's whole intent into one click: when the test
   * lands red it is committed — pre-authorized by this request — and the
   * build starts against it with the given options. One decision per task,
   * at the build's gate, with the committed test in the diff. */
  testStart(
    projectId: string,
    taskId: string,
    duckling = "",
    chain?: {
      thenBuild: boolean;
      /** The TEST phase's own mode and seats — independent of the build's,
       * because a person who pairs the build does not owe the test a pair. */
      testMode?: string;
      testDucklings?: string[];
      mode?: string;
      ducklings?: string[];
      maxTokens?: number;
      agentTurns?: number;
    },
  ) {
    return this.request<Run>("POST", `/v1/projects/${projectId}/tests`, {
      task_id: taskId,
      duckling,
      mode: chain?.testMode ?? "",
      ducklings: chain?.testDucklings ?? [],
      then_build: chain?.thenBuild ?? false,
      build: chain?.thenBuild
        ? {
            task_id: taskId,
            mode: chain.mode || "solo",
            ducklings: chain.ducklings ?? [],
            ...(chain.maxTokens ? { budget: { max_tokens: chain.maxTokens } } : {}),
            ...(chain.agentTurns ? { agent_turns: chain.agentTurns } : {}),
          }
        : undefined,
    });
  }

  /** Withdraw a committed failing test: the engine reverts its commit —
   * deterministic, no model — freeing the task and the project's queue. The
   * other exit from a broken chain, beside building until green. */
  testRetire(projectId: string, taskId: string) {
    return this.request<Run>(
      "POST",
      `/v1/projects/${projectId}/tasks/${encodeURIComponent(taskId)}/retire-test`,
    );
  }

  /** Start a conversation with a chosen duckling about a bug or task — its
   * history rides as context, its tools are read-only, the run view is the
   * chat panel. */
  chatStart(projectId: string, req: { duckling: string; aboutKind: string; aboutId: string; message: string }) {
    return this.request<Run>("POST", `/v1/projects/${projectId}/chats`, {
      duckling: req.duckling, about_kind: req.aboutKind, about_id: req.aboutId, message: req.message,
    });
  }
  /** Send the next message in a paused chat. */
  chatSend(runId: string, message: string) {
    return this.request<Run>("POST", `/v1/runs/${runId}/chat`, { message });
  }
  /** End a chat as finished — a done consultation is not an abort. */
  chatEnd(runId: string) {
    return this.request<Run>("POST", `/v1/runs/${runId}/chat/end`);
  }

  /** The app's run configuration and managed-process state. */
  appStatus(projectId: string) {
    return this.request<AppStatus>("GET", `/v1/projects/${projectId}/app`);
  }
  /** Start the app via run.command as an engine-managed process. */
  appStart(projectId: string) {
    return this.request<AppStatus>("POST", `/v1/projects/${projectId}/app/start`);
  }
  /** Stop the managed app — kills the whole process group. */
  appStop(projectId: string) {
    return this.request<void>("POST", `/v1/projects/${projectId}/app/stop`);
  }

  /** File the run's final reviewer findings as bug reports, with provenance.
   * Idempotent on the engine: a second filing is refused, not duplicated. */
  runFileFindings(id: string) {
    return this.request<{ items: { id: string; title: string }[] }>(
      "POST",
      `/v1/runs/${id}/findings/file`,
    );
  }

  /** Remove one budget cap from a live run — one-way, recorded on the run.
   * Per-cap on purpose: lifting tokens leaves the dollar ceiling standing.
   * "calls" is the per-reply call cap inside the agent loop; its lift lands
   * mid-reply, on the loop's very next call. */
  runBudgetLift(id: string, kind: "tokens" | "usd" | "turns" | "wallclock" | "calls") {
    return this.request<Run>("POST", `/v1/runs/${id}/budget/lift`, { kind });
  }

  /** Review an accepted task. */
  reviewStart(projectId: string, taskId: string) {
    return this.request<Run>("POST", `/v1/projects/${projectId}/reviews`, { task_id: taskId });
  }

  /** The roster as it will actually be used — including the roles the engine
   * filled in, which the file does not declare. */
  /** Who would play each role — for a mode, when named, so the answer matches
   * the run about to start rather than the bare roster a line-up overrides. */
  roster(projectId: string, mode = "") {
    const q = mode ? `?mode=${encodeURIComponent(mode)}` : "";
    return this.request<{ entries: RosterEntry[] | null; warning?: string }>(
      "GET",
      `/v1/projects/${projectId}/roster${q}`,
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
  /** The budget every run starts with. It was invisible and immutable: a run
   * that hit the ceiling failed with a number nobody had chosen. */
  budgetDefaults() {
    return this.request<BudgetView>("GET", "/v1/defaults/budget");
  }
  budgetDefaultsSet(body: BudgetView) {
    return this.request<BudgetView>("PUT", "/v1/defaults/budget", body);
  }
  /** Rounds per mode and the per-turn model-call cap. Both lived only in the
   * scripts and the engine config, so changing how many times a reviewer got to
   * push back meant editing Go and rebuilding. */
  modeDefaults() {
    return this.request<ModeDefaultsView>("GET", "/v1/defaults/modes");
  }
  modeDefaultsSet(body: ModeDefaultsView) {
    return this.request<ModeDefaultsView>("PUT", "/v1/defaults/modes", body);
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
  /** Every model call this run made: what was sent, what came back, what it
   * cost. The one place that shows what a model was actually given — a prompt
   * is assembled from a task, a spec, a transcript and a toolbelt, and when the
   * answer is wrong the question is usually where to look. */
  runLLM(id: string, fromSeq = 0) {
    // Query string built apart: the desktop-coverage pin reads the route
    // literal, and a ternary inside it hid the path from the matcher.
    const qs = fromSeq > 0 ? `?from_seq=${fromSeq}` : "";
    return this.request<{ items: LLMCall[] | null; total: number }>(
      "GET",
      `/v1/runs/${id}/llm` + qs,
    ).then((r) => r.items ?? []);
  }
  /** Resume a run the engine's own restart or shutdown paused. A human gate is
   * not a resume point — it is answered, not continued. */
  runResume(id: string) {
    return this.request<Run>("POST", `/v1/runs/${id}/resume`);
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
    opts: { from?: string; mode?: string; revise?: string; rounds?: number; adopt?: boolean } = {},
  ) {
    return this.request<Run>("POST", `/v1/projects/${projectId}/stages/${stage}`, {
      stage,
      from: opts.from ?? "",
      // Set only when revising: it turns the run from "write this document"
      // into "edit this one", which is the answer to a draft that is almost
      // right.
      revise: opts.revise ?? "",
      // Intake only: survey the tree into the requirements the code already
      // satisfies, instead of interviewing about an idea.
      adopt: opts.adopt ?? false,
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
  /** `proposed` names the stages whose pending proposal was checked instead of
   * the approved artifact — the findings are about what you are deciding on,
   * not what you already accepted. */
  /** Discard a pending proposal. A rejected one stays on disk by design
   * (05 §1.1) — a failed attempt is a record — so letting it go is a person's
   * explicit act, never a side effect of the reject. */
  artifactDiscard(projectId: string, kind: string) {
    return this.request<{ discarded: string }>(
      "DELETE",
      `/v1/projects/${projectId}/artifacts/${kind}/proposal`,
    );
  }
  /** The development report: narrative from the approved requirements, the
   * requirement→spec→task matrix with statuses, bug fixes, releases, spine
   * health. Deterministic — no model writes the record. */
  traceReport(projectId: string) {
    return this.request<{ rendered: string }>("GET", `/v1/projects/${projectId}/trace/report`);
  }

  traceCheck(projectId: string) {
    return this.request<{ errors: TraceError[] | null; proposed?: string[] | null }>(
      "GET",
      `/v1/projects/${projectId}/trace/check`,
    ).then((r) => ({ errors: r.errors ?? [], proposed: r.proposed ?? [] }));
  }
  traceShow(projectId: string, id: string) {
    return this.request<Record<string, unknown>>("GET", `/v1/projects/${projectId}/trace/${id}`);
  }
  /** The first task ready to be started: dependencies accepted, not blocked,
   * not already being run. The board shows every task's state and never
   * answered the question a person actually arrives with. */
  taskNext(projectId: string) {
    return this.request<Task | Record<string, never>>(
      "GET",
      `/v1/projects/${projectId}/tasks/next`,
    ).then((t) => ("id" in t && t.id ? (t as Task) : null));
  }
  bugs(projectId: string, openOnly = false) {
    const q = openOnly ? "?open=true" : "";
    return this.request<{ items: Bug[] | null; total: number }>(
      "GET",
      `/v1/projects/${projectId}/bugs${q}`,
    ).then((r) => r.items ?? []);
  }
  /** File a report. Severity is taken as given: a reporter saying "critical"
   * may be wrong, but a tool that quietly downgrades what it was told is a tool
   * nobody reports to twice — triage is where that judgement belongs. */
  /** Attach one file (as base64) to a bug. The screenshot that says what a
   * paragraph cannot — and what a vision triager gets shown. */
  bugAttach(projectId: string, bugId: string, filename: string, dataBase64: string) {
    return this.request<{ items: string[] }>(
      "POST",
      `/v1/projects/${projectId}/bugs/${encodeURIComponent(bugId)}/attachments`,
      { filename, data: dataBase64 },
    );
  }
  /** Fetch one attachment as a blob URL — <img src> cannot carry the auth
   * header, so the bytes come through the client and the URL is local. */
  async bugAttachmentUrl(projectId: string, bugId: string, name: string): Promise<string> {
    const f = this.opts.fetchFn ?? fetch;
    const headers: Record<string, string> = { Authorization: `Bearer ${this.opts.token}` };
    if (this.opts.version) headers["X-Ducklab-Client"] = this.opts.version;
    const resp = await f(
      `${this.opts.baseUrl}/v1/projects/${projectId}/bugs/${encodeURIComponent(bugId)}/attachments/${encodeURIComponent(name)}`,
      { headers },
    );
    if (!resp.ok) throw new Error(`attachment ${name}: ${resp.status}`);
    return URL.createObjectURL(await resp.blob());
  }

  bugAdd(projectId: string, body: { title: string; body?: string; severity?: string }) {
    return this.request<Bug>("POST", `/v1/projects/${projectId}/bugs`, {
      severity: "normal",
      reporter: "human",
      source: "desktop",
      ...body,
    });
  }
  /** Triage every open report: severity, suspected files, duplicates. Returns
   * the run doing it, which is watchable like any other. */
  triageBugs(projectId: string) {
    return this.request<Run>("POST", `/v1/projects/${projectId}/bugs/triage`);
  }
  /** Correct what a report says. A bug could be moved, triaged and promoted but
   * never edited, so a typo or a missing detail lived as long as the bug did —
   * and the triager, and then the implementer, worked from it. */
  bugEdit(projectId: string, bugId: string, body: { title?: string; body?: string; severity?: string }) {
    return this.request<Bug>("PUT", `/v1/projects/${projectId}/bugs/${bugId}`, body);
  }
  /** Remove a task from the plan. The engine refuses once a run has named it,
   * because the runs, the reports and the spine all point at it. */
  taskRemove(projectId: string, taskId: string) {
    return this.request<{ removed: string; bug?: string; bug_status?: string }>(
      "DELETE",
      `/v1/projects/${projectId}/tasks/${taskId}`,
    );
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
  /** Start a bench without holding the request open: the engine validates the
   * picks synchronously — a misspelled duckling is refused here, not found in a
   * log twenty minutes later — then runs every cell as an ordinary run. The
   * result appears in benchList when it finishes. */
  benchStart(body: { ducklings: string[]; modes: string[]; suite?: string }) {
    return this.request<{ started: boolean; suite: string; cells: number }>(
      "POST",
      "/v1/bench/start",
      body,
    );
  }
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
  releasePlan(projectId: string, bump: string) {
    return this.request<Run>("POST", `/v1/projects/${projectId}/releases`, { bump });
  }

  releaseCut(projectId: string, version: string) {
    return this.request<Record<string, unknown>>(
      "POST",
      `/v1/projects/${projectId}/releases/${version}/cut`,
    );
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

  /** The project's suggested next steps — the guide. Computed by the engine
   * from the real state (documents, tasks, bugs, paused runs), in the order
   * the loop itself would take them, each pointing at an action that already
   * exists. Deterministic: guidance that reads the state cannot go stale the
   * way a tutorial does. */
  projectNext(projectId: string) {
    return this.request<{ items: NextStep[] | null; total: number }>(
      "GET",
      `/v1/projects/${projectId}/next`,
    ).then((r) => r.items ?? []);
  }
}
