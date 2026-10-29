// ducklab-bell: an OpenCode (2.x) server plugin that makes one OpenCode
// session the coordinator of a Ducklab project.
//
// Ducklab announces what an operator acts on (a run asks, pauses at its gate,
// fails, ends) on the engine's event stream. This plugin listens to that
// stream from inside the OpenCode server and wakes the configured session
// with a short [Ducklab] message, so the agent there looks with its ducklab
// MCP tools and continues — instead of polling, or waiting for the person to
// notice. In "act" mode it also carries the coordinator policy (policy.md)
// into that session's system prompt and refuses the waiting/polling tool
// calls the policy forbids.
//
// What the coordinator arc learned, kept here as rules:
//  - One wake at a time. OpenCode 2.x queues a prompt sent to a busy session
//    in its inbox and gives it its own turn; we still wait for each wake's
//    turn to end before sending the next batch.
//  - Bursts are coalesced (5 s) and wakes are capped per hour; a hold is
//    logged, never silent (a silent hold read as a broken bell).
//  - engine.json is re-read on every reconnect, so engine restarts are
//    followed; what a restart or a reload missed comes back through the
//    catch-up below rather than through a stream cursor.
//  - On every (re)connect the plugin catches up: runs still waiting for a
//    decision since the last wake are announced, so a wake lost to a reload,
//    a disconnect or a hold is not lost for good.
//  - Policy alone did not stop a local model from `sleep 90`-polling the run
//    files; in act mode those calls are refused with an instruction. The
//    refusal is a guard rail against habit, not a barrier: the engine's
//    credential rules are the barrier.
//
// Configuration: plugin options in the OpenCode config (see README.md), or a
// bell.json next to this file. Options win.
import { appendFileSync, existsSync, readFileSync, unlinkSync, writeFileSync } from "node:fs"
import { homedir, platform } from "node:os"
import { dirname, isAbsolute, join } from "node:path"
import { fileURLToPath } from "node:url"

const HERE = dirname(fileURLToPath(import.meta.url))

// Same doorbell set as the engine's outbound webhook (internal/service/notify.go).
const WAKE = new Set([
  "human_needed", "run_end", "autopilot", "run_paused", "question_asked",
  "notification", "distress", "failure_streak", "repetition_loop", "budget_pause",
])
const DEBOUNCE_MS = 5_000
const SILENCE_MS = 60_000 // the engine heartbeats every 15 s
const TURN_TIMEOUT_MS = 30 * 60_000

const PREAMBLES: Record<string, string> = {
  ask:
    "[Ducklab] New events in this project. Check what happened with the ducklab tools " +
    "(status, run_get). Do not change anything without the user's approval: summarize in " +
    "2-3 lines and propose the next action.",
  act:
    "[Ducklab] New events in this project. Check what happened with the ducklab tools " +
    "(status, run_get) and continue your errand under the Ducklab coordinator policy. If a " +
    "decision needs the user, start a line with 'NEEDS USER:'.",
}

// refusedCall reports why a tool call breaks the coordinator's waiting
// rules, or "" when it does not. A guard rail against a model's habit, not a
// barrier: it reads every argument a call can point somewhere with, and errs
// toward refusing — a coordinator has no business waiting in a shell or
// reading Ducklab's own state.
const waits = /\b(sleep|watch)\b|\btail\s+-\w*[fF]|\bread\s+-\w*t/
const ducklabState = /\.ducklab\b|state[\\/]+ducklab|engine\.json|human\.tok/
// discards matches a ducklab decide that throws a run's work away. The
// policy said "never abort or reject without the user"; a local coordinator
// quoted the rule in its reasoning and aborted anyway, twice (2026-10-29,
// T-030). Code Mode calls arrive as the `execute` tool with the call in its
// code, so the whole input is searched.
const discards = /\bdecide\b[\s\S]*\baction\b[\\"']*\s*:\s*[\\"']*(abort|reject)\b/

function refusedCall(q: any): string {
  const i = q.input ?? {}
  if (discards.test(JSON.stringify(i))) return "discard"
  // A direct MCP call (no Code Mode): the tool is ducklab's decide.
  if (/decide$/.test(String(q.tool ?? "")) && /^(abort|reject)$/.test(String(i.action ?? ""))) return "discard"
  const where = [i.path, i.filePath, i.workdir, i.cwd, i.pattern, i.include].filter((v) => typeof v === "string").join(" ")
  const command = q.tool === "shell" && typeof i.command === "string" ? i.command : ""
  if (command && waits.test(command)) return "waiting"
  if (ducklabState.test(`${command} ${where}`)) return "ducklab state"
  return ""
}

const DISCARD_REFUSAL =
  "Refused by the Ducklab coordinator policy: aborting or rejecting a run throws its work away, and that is " +
  "the user's decision. Do not retry it. Tell the user in 2-3 lines what you saw (from run_get, not only the " +
  "distress event) and propose the abort; they decide, in the desktop or by telling you."

const REFUSAL =
  "Refused by the Ducklab coordinator policy: do not wait or poll, and do not read .ducklab/ files. " +
  "End your turn now; Ducklab wakes you with a [Ducklab] message when the run needs you or ends. " +
  "To check a run, use the ducklab run_get tool."

interface Config {
  session: string
  project: string
  mode: "ask" | "act"
  language?: string
  maxPerHour: number
  policy?: string
}

// stateDir is where the engine writes engine.json (internal/xplat).
function stateDir(): string {
  if (platform() === "darwin") return join(homedir(), "Library", "Application Support", "ducklab")
  if (platform() === "win32") return join(process.env.LocalAppData ?? homedir(), "ducklab")
  return join(process.env.XDG_STATE_HOME || join(homedir(), ".local", "state"), "ducklab")
}

function loadConfig(options: Record<string, unknown> | undefined, log: (...p: unknown[]) => void): Config {
  let file: Record<string, unknown> = {}
  const path = join(HERE, "bell.json")
  if (existsSync(path)) {
    try {
      file = JSON.parse(readFileSync(path, "utf8"))
    } catch (e: any) {
      log(`bell.json unreadable, ignored: ${e?.message ?? e}`)
    }
  }
  // A null option does not erase the file's value.
  const given = Object.fromEntries(Object.entries(options ?? {}).filter(([, v]) => v !== null && v !== undefined))
  const o: Record<string, unknown> = { ...file, ...given }
  if (o.mode !== undefined && o.mode !== "ask" && o.mode !== "act") log(`mode ${JSON.stringify(o.mode)} is not ask or act; using ask`)
  return {
    session: String(o.session ?? ""),
    project: String(o.project ?? ""),
    mode: o.mode === "act" ? "act" : "ask",
    language: typeof o.language === "string" && o.language ? o.language : undefined,
    maxPerHour: typeof o.maxPerHour === "number" && o.maxPerHour > 0 ? o.maxPerHour : 30,
    policy: typeof o.policy === "string" && o.policy ? o.policy : undefined,
  }
}

// sleep resolves after ms or on abort, and leaves no listener behind either
// way: the waker sleeps every second for the plugin's life.
const sleep = (ms: number, signal?: AbortSignal) =>
  new Promise<void>((resolve) => {
    if (signal?.aborted) return resolve()
    const onAbort = () => (clearTimeout(t), resolve())
    const t = setTimeout(() => (signal?.removeEventListener("abort", onAbort), resolve()), ms)
    signal?.addEventListener("abort", onAbort, { once: true })
  })

function describe(ev: any) {
  const d = ev.data ?? {}
  let detail = d.detail ?? d.verdict ?? d.reason ?? d.question ?? ""
  if (typeof detail === "object") detail = JSON.stringify(detail).slice(0, 120)
  return (
    `- ${ev.type} · run ${ev.run_id || "-"} · project ${ev.project_id || "-"}` +
    (detail ? ` · ${String(detail).slice(0, 160)}` : "")
  )
}

// One owner per session across OpenCode processes and reloads: a lock file
// holding the owner's pid; a dead owner's lock is taken over. Within one
// process, owners maps the lock to the plugin instance holding it — OpenCode
// can load a second instance before unloading the first, and a per-process
// "owned" flag let both wake the session.
type Owners = Map<string, symbol>
function tryLock(path: string, owners: Owners, log: (...p: unknown[]) => void): boolean {
  for (let i = 0; i < 2; i++) {
    try {
      writeFileSync(path, String(process.pid), { flag: "wx" })
      // Two processes that both found a dead owner can both unlink and
      // create; only the one whose pid the file still holds owns it.
      const start = Date.now()
      while (Date.now() - start < 50) {}
      return readFileSync(path, "utf8").trim() === String(process.pid)
    } catch (e: any) {
      if (e?.code && e.code !== "EEXIST") {
        log(`cannot take the lock ${path}: ${e.code}`)
        return false
      }
      let pid = 0
      try {
        pid = Number(readFileSync(path, "utf8").trim())
      } catch {}
      let alive = false
      if (pid > 0 && pid !== process.pid) {
        try {
          process.kill(pid, 0)
          alive = true
        } catch (e: any) {
          alive = e?.code === "EPERM" // exists, owned by someone else
        }
      }
      // Our own pid with no live instance holding it is a lock left by an
      // earlier load in this process.
      if (alive || (pid === process.pid && owners.has(path))) return false
      try {
        unlinkSync(path)
      } catch {}
    }
  }
  return false
}

export default {
  id: "ducklab-bell",
  setup(ctx: any) {
    const state = stateDir()
    const logPath = join(state, "bell.log")
    const log = (...parts: unknown[]) => {
      try {
        appendFileSync(logPath, `${new Date().toISOString()} [bell pid=${process.pid}] ${parts.map(String).join(" ")}\n`)
      } catch {}
    }
    const cfg = loadConfig(ctx.options, log)
    if (!cfg.session || !cfg.project) {
      log("not configured: set the session and project options (README.md); idle")
      return
    }
    const sessionID = cfg.session
    const preamble = PREAMBLES[cfg.mode] + (cfg.language ? ` Reply in ${cfg.language}.` : "")
    const lockPath = join(state, `bell-${sessionID}.lock`)
    const owners: Owners = ((globalThis as any).__ducklabBellOwners ??= new Map())
    const me = Symbol("ducklab-bell")
    const owns = () => owners.get(lockPath) === me
    const stop = new AbortController()
    const signal = stop.signal

    let waking = false
    let holding = false
    let pending: any[] = []
    let firstPendingAt = 0
    let wakes: number[] = []

    log(`loaded at ${ctx.location?.directory} for ${sessionID} (project ${cfg.project}, mode ${cfg.mode})`)

    const add = (ev: any) => {
      pending.push(ev)
      firstPendingAt ||= Date.now()
    }

    // The last time a wake went out, kept across reloads: catch-up announces
    // only what has waited since.
    const lastWakeKey = `last-wake:${sessionID}`
    const lastWake = async (): Promise<string> => String((await ctx.storage.get(lastWakeKey).catch(() => "")) ?? "")

    async function wake(batch: any[]) {
      const lines = batch.slice(0, 20).map(describe).join("\n")
      const more = batch.length > 20 ? `\n(+${batch.length - 20} more)` : ""
      const text = `${preamble}\n\n${lines}${more}`
      const t0 = Date.now()
      log(`waking ${sessionID} with ${batch.length} event(s)`)
      const sentAt = new Date().toISOString()
      await ctx.session.prompt({ sessionID, text })
      // Only a delivered wake moves the catch-up mark: one that failed must
      // still be found on the next connect.
      await ctx.storage.set(lastWakeKey, sentAt).catch(() => {})
      await Promise.race([ctx.session.wait({ sessionID }), sleep(TURN_TIMEOUT_MS, signal)])
      log(`wake done in ${Math.round((Date.now() - t0) / 1000)}s`)
    }

    async function waker() {
      while (!signal.aborted) {
        await sleep(1_000, signal)
        if (!owns() || waking || !pending.length || Date.now() - firstPendingAt < DEBOUNCE_MS) continue
        const now = Date.now()
        wakes = wakes.filter((t) => now - t < 3_600_000)
        if (wakes.length >= cfg.maxPerHour) {
          if (!holding) log(`holding ${pending.length} event(s): ${cfg.maxPerHour} wakes in the last hour`)
          holding = true
          continue
        }
        holding = false
        const batch = pending
        pending = []
        firstPendingAt = 0
        wakes.push(now)
        waking = true
        try {
          await wake(batch)
        } catch (e: any) {
          // Not dropped: the batch goes back first in line for the next try.
          log("wake failed, will retry:", e?.message ?? e)
          pending = [...batch, ...pending]
          firstPendingAt ||= Date.now()
        } finally {
          waking = false
        }
      }
    }

    // The coordinator policy rides in the session's system prompt (act mode
    // only, this session only), and the calls it forbids are refused. The
    // context hook is how OpenCode's own plugins add system text; its entries
    // are LLM.SystemPart objects — a bare string fails schema validation.
    // Every instance installs it, owner of the bell or not: a turn the person
    // types in a process that does not ring is still the coordinator's turn.
    // Compaction gets it too, so a summary is written under the same rules.
    async function installPolicy() {
      if (cfg.mode !== "act") return
      try {
        const path = cfg.policy ? (isAbsolute(cfg.policy) ? cfg.policy : join(HERE, cfg.policy)) : join(HERE, "policy.md")
        const policy = readFileSync(path, "utf8")
        const carry = (r: any) => {
          if (r.sessionID === sessionID) r.system.push({ type: "text", text: policy })
        }
        await ctx.session.hook("context", carry)
        await ctx.session.hook("compaction", carry)
        await ctx.tool.hook("execute.before", (q: any) => {
          if (q.sessionID !== sessionID) return
          const why = refusedCall(q)
          if (why) {
            log(`refused ${q.tool} (${why}): ${JSON.stringify(q.input ?? {}).slice(0, 100)}`)
            throw new Error(why === "discard" ? DISCARD_REFUSAL : REFUSAL)
          }
        })
        log("policy installed")
      } catch (e: any) {
        log("policy install failed:", e?.message ?? e)
      }
    }

    // catchUp announces runs that have waited for a decision since the last
    // wake: whatever the stream carried while this plugin was not listening
    // (a reload, an engine restart, a hold) is not lost.
    async function catchUp(base: string, token: string) {
      try {
        const res = await fetch(`${base}/v1/runs?project=${encodeURIComponent(cfg.project)}&status=paused`, {
          headers: { Authorization: `Bearer ${token}` },
          signal,
        })
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        const body: any = await res.json()
        let since = await lastWake()
        if (!since) {
          // First load for this session: start the mark now rather than
          // announce every gate the project ever left open.
          since = new Date().toISOString()
          await ctx.storage.set(lastWakeKey, since).catch(() => {})
          log("first connect for this session: catch-up starts now")
        }
        const queued = new Set(pending.map((e) => e.run_id))
        // An empty list answers {"items": null}.
        for (const r of body?.items ?? []) {
          // A consultant chat waiting on the person is a conversation, not a decision.
          if (!r?.pending_kind || r.pending_kind === "chat" || queued.has(r.id)) continue
          if (since && r.pending_since && r.pending_since <= since) continue
          add({
            type: "human_needed", run_id: r.id, project_id: cfg.project,
            data: { detail: `${r.verdict || r.pending_kind} — waiting (${r.pending_kind}); found on connect` },
          })
          log("caught up", r.id, r.pending_kind)
        }
      } catch (e: any) {
        if (!signal.aborted) log("catch-up failed:", e?.message ?? e)
      }
    }

    async function listen() {
      let backoff = 1_000
      while (!signal.aborted) {
        if (!owns()) {
          if (tryLock(lockPath, owners, log)) {
            owners.set(lockPath, me)
            log("owns", sessionID)
          } else {
            await sleep(30_000, signal)
            continue
          }
        }
        const conn = new AbortController()
        const onStop = () => conn.abort()
        signal.addEventListener("abort", onStop, { once: true })
        let silence: ReturnType<typeof setTimeout> | undefined
        const arm = () => {
          clearTimeout(silence)
          silence = setTimeout(() => conn.abort(), SILENCE_MS)
        }
        try {
          const info = JSON.parse(readFileSync(join(state, "engine.json"), "utf8"))
          const base = `http://127.0.0.1:${info.port}`
          arm()
          const res = await fetch(`${base}/v1/events?project=${encodeURIComponent(cfg.project)}`, {
            headers: { Authorization: `Bearer ${info.token}`, Accept: "text/event-stream" },
            signal: conn.signal,
          })
          if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`)
          log(`connected to engine ${info.version} on :${info.port}`)
          backoff = 1_000
          void catchUp(base, info.token)
          const decoder = new TextDecoder()
          let buf = ""
          let data: string[] = []
          for await (const chunk of res.body as any) {
            arm()
            buf += decoder.decode(chunk, { stream: true })
            let nl: number
            while ((nl = buf.indexOf("\n")) >= 0) {
              const line = buf.slice(0, nl).replace(/\r$/, "")
              buf = buf.slice(nl + 1)
              if (line.startsWith("data:")) data.push(line.slice(5).trim())
              else if (line === "" && data.length) {
                try {
                  const ev = JSON.parse(data.join("\n"))
                  if (WAKE.has(ev.type)) {
                    log("event", describe(ev))
                    add(ev)
                  }
                } catch {}
                data = []
              }
            }
          }
          throw new Error("stream closed")
        } catch (e: any) {
          if (signal.aborted) break
          log(`stream lost: ${e?.message ?? e}; retry in ${backoff / 1000}s`)
          await sleep(backoff, signal)
          backoff = Math.min(backoff * 2, 30_000)
        } finally {
          clearTimeout(silence)
          signal.removeEventListener("abort", onStop)
        }
      }
    }

    void installPolicy()
    void waker()
    void listen()

    return () => {
      stop.abort()
      if (owns()) {
        owners.delete(lockPath)
        try {
          unlinkSync(lockPath)
        } catch {}
      }
      log("unloaded")
    }
  },
}
