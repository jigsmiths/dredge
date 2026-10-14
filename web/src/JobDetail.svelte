<script lang="ts">
  import { api, active, size, type JobDetail, type Track } from "./api";

  let { id }: { id: number } = $props();

  let job = $state<JobDetail | null>(null);
  let error = $state("");

  async function load() {
    try {
      job = await api.get(id);
      error = "";
    } catch (e) {
      error = (e as Error).message;
    }
  }

  $effect(() => {
    load();
    const t = setInterval(load, 2000);
    return () => clearInterval(t);
  });

  async function act(f: (id: number) => Promise<unknown>) {
    try {
      await f(id);
      await load();
    } catch (e) {
      error = (e as Error).message;
    }
  }

  async function remove() {
    try {
      await api.remove(id);
      location.hash = "#/";
    } catch (e) {
      error = (e as Error).message;
    }
  }

  const by = (ts: Track[], ...st: string[]) => ts.filter((t) => st.includes(t.status));
  const who = (t: Track) => t.artists.join(", ");
  const why: Record<string, string> = {
    no_match: "no match",
    ambiguous: "ambiguous",
    not_stocked: "not on Monochrome",
    failed: "failed",
  };
</script>

<p><a href="#/">&larr; All jobs</a></p>

{#if job}
  <h1>{job.name || job.url}</h1>
  <p class="muted">
    {job.kind} · <span class="chip {job.status}">{job.status}</span>
    {#if job.total}· {job.total} tracks{/if}
  </p>
  {#if job.error}<p class="error">{job.error}</p>{/if}

  <div class="actions">
    {#if active(job)}
      <button onclick={() => act(api.cancel)}>Cancel</button>
    {:else}
      <button onclick={() => act(api.retry)}>Retry</button>
      <button class="quiet" onclick={remove}>Remove from history</button>
    {/if}
  </div>

  {@const now = by(job.tracks, "downloading")}
  {@const next = by(job.tracks, "pending")}
  {@const bad = by(job.tracks, "failed", "not_stocked", "no_match", "ambiguous")}
  {@const ok = by(job.tracks, "done", "have", "duplicate")}

  {#if job.status === "resolving"}
    <p class="muted">Looking up the tracks and checking the library...</p>
  {/if}

  {#if now.length}
    <h2>Downloading ({now.length})</h2>
    {#each now as t (t.pos)}
      <div class="t"><span>{t.title}</span><span class="muted">{who(t)}</span></div>
    {/each}
  {/if}

  {#if next.length}
    <h2>Still to do ({next.length})</h2>
    {#each next as t (t.pos)}
      <div class="t"><span>{t.title}</span><span class="muted">{who(t)}</span></div>
    {/each}
  {/if}

  {#if bad.length}
    <h2>Not fetched ({bad.length})</h2>
    {#each bad as t (t.pos)}
      <div class="t">
        <span>{t.title}</span>
        <span class="muted">{who(t)}</span>
        <span class="bad">{why[t.status]}{t.reason ? `: ${t.reason}` : ""}</span>
      </div>
    {/each}
  {/if}

  {#if ok.length}
    <h2>Passed ({ok.length})</h2>
    {#each ok as t (t.pos)}
      <div class="t">
        <span>{t.title}</span>
        <span class="muted">{who(t)}</span>
        <span class="muted">
          {t.status === "done" ? `downloaded, ${size(t.bytes)}` : t.status === "have" ? "already had" : "repeat"}
        </span>
      </div>
    {/each}
  {/if}
{:else if error}
  <p class="error">{error}</p>
{:else}
  <p class="muted">Loading...</p>
{/if}

<style>
  a {
    color: #7bc;
  }
  h1 {
    margin-top: 0.5rem;
  }
  h2 {
    font-size: 0.85rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: #9a9a9a;
    margin: 1.6rem 0 0.4rem;
  }
  .actions {
    display: flex;
    gap: 0.5rem;
    margin: 1rem 0;
  }
  button {
    padding: 0.45rem 0.9rem;
    background: #3a7;
    color: #fff;
    border: 0;
    border-radius: 6px;
    cursor: pointer;
  }
  button.quiet {
    background: #333;
  }
  .t {
    display: grid;
    grid-template-columns: 2fr 1.5fr 1.5fr;
    gap: 0.6rem;
    padding: 0.35rem 0;
    border-bottom: 1px solid #222;
    font-size: 0.92rem;
  }
  .t span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .muted {
    color: #9a9a9a;
  }
  .bad,
  .error {
    color: #cf6679;
  }
  .chip {
    font-size: 0.75rem;
    padding: 0.1rem 0.5rem;
    border-radius: 99px;
    background: #333;
    color: #e0e0e0;
  }
  .chip.running,
  .chip.resolving {
    background: #1d5a8a;
  }
  .chip.done {
    background: #236b45;
  }
  .chip.failed {
    background: #8a2d3b;
  }
</style>
