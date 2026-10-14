<script lang="ts">
  import { api, apiKey, Unauthorized, active, ago, clock, isLink, type Hit, type Job } from "./api";
  import JobDetail from "./JobDetail.svelte";

  let jobs = $state<Job[]>([]);
  let link = $state("");
  let error = $state("");
  let needKey = $state(false);
  let keyInput = $state("");
  let selected = $state<number | null>(null);
  let hits = $state<Hit[] | null>(null);
  let searched = $state("");
  let busy = $state(false);
  let version = $state("");

  fetch("/health")
    .then((r) => r.json())
    .then((h) => (version = h.version ?? ""))
    .catch(() => {});

  const fromHash = () => {
    const m = location.hash.match(/^#\/jobs\/(\d+)$/);
    selected = m ? Number(m[1]) : null;
  };
  fromHash();

  async function refresh() {
    try {
      jobs = await api.list();
      needKey = false;
      error = "";
    } catch (e) {
      if (e instanceof Unauthorized) needKey = true;
      else error = (e as Error).message;
    }
  }

  $effect(() => {
    refresh();
    const t = setInterval(refresh, 3000);
    window.addEventListener("hashchange", fromHash);
    return () => {
      clearInterval(t);
      window.removeEventListener("hashchange", fromHash);
    };
  });

  function fail(e: unknown) {
    if (e instanceof Unauthorized) needKey = true;
    else error = (e as Error).message;
  }

  // A link starts a download; anything else searches, and the user picks.
  async function go(e: SubmitEvent) {
    e.preventDefault();
    const text = link.trim();
    busy = true;
    try {
      if (isLink(text)) {
        const j = await api.submit(text);
        link = "";
        hits = null;
        error = "";
        await refresh();
        location.hash = `#/jobs/${j.id}`;
      } else {
        hits = await api.search(text);
        searched = text;
        error = "";
      }
    } catch (e) {
      fail(e);
    } finally {
      busy = false;
    }
  }

  async function pick(h: Hit) {
    try {
      const j = await api.submitTrack(h);
      link = "";
      hits = null;
      error = "";
      await refresh();
      location.hash = `#/jobs/${j.id}`;
    } catch (e) {
      fail(e);
    }
  }

  function saveKey(e: SubmitEvent) {
    e.preventDefault();
    apiKey.set(keyInput.trim());
    keyInput = "";
    refresh();
  }

  const label = (j: Job) => j.name || j.url.replace("https://open.spotify.com/", "");
  const c = (j: Job, k: string) => j.counts[k] ?? 0;
  const finished = (j: Job) => j.total - c(j, "pending") - c(j, "downloading");
  const problems = (j: Job) =>
    c(j, "failed") + c(j, "not_stocked") + c(j, "lossy_only") + c(j, "no_match") + c(j, "ambiguous");
  const current = $derived(jobs.filter(active));
  const history = $derived(jobs.filter((j) => !active(j)));
</script>

<main>
  <header>
    <h1><a href="#/">dredge</a></h1>
    {#if version}
      <a
        class="version"
        href={version === "dev" ? undefined : `https://github.com/jigsmiths/dredge/releases/tag/v${version}`}
        target="_blank"
        rel="noopener noreferrer">{version}</a
      >
    {/if}
  </header>

  {#if needKey}
    <form class="row" onsubmit={saveKey}>
      <input type="password" placeholder="API key" bind:value={keyInput} />
      <button>Save</button>
    </form>
  {/if}

  {#if selected !== null}
    <JobDetail id={selected} />
  {:else}
    <form class="row" onsubmit={go}>
      <input placeholder="Search for a song, or paste a Spotify link" bind:value={link} required />
      <button disabled={busy}>{isLink(link) ? "Download" : "Search"}</button>
    </form>
    {#if error}<p class="error">{error}</p>{/if}

    {#if hits}
      <h2>Results for "{searched}"</h2>
      {#each hits as h (h.isrc)}
        <div class="hit">
          <span class="name">{h.title}</span>
          <span class="muted">{h.artists.join(", ")} · {clock(h.duration_ms)}</span>
          <button onclick={() => pick(h)}>Download</button>
        </div>
      {:else}
        <p class="muted">Nothing downloadable found.</p>
      {/each}
    {/if}

    <h2>Now</h2>
    {#each current as j (j.id)}
      <a class="job" href={`#/jobs/${j.id}`}>
        <span class="name">{label(j)}</span>
        <span class="chip {j.status}">{j.status}</span>
        {#if j.total}
          <progress max={j.total} value={finished(j)}></progress>
          <span class="meta">{finished(j)} of {j.total}</span>
        {/if}
      </a>
    {:else}
      <p class="muted">Nothing running.</p>
    {/each}

    <h2>History</h2>
    {#each history as j (j.id)}
      <a class="job" href={`#/jobs/${j.id}`}>
        <span class="name">{label(j)}</span>
        <span class="chip {j.status}">{j.status}</span>
        <span class="meta">
          {c(j, "done")} downloaded, {c(j, "have")} already had{#if problems(j)}, {problems(j)} not
            fetched{/if}
          · {ago(j.finished_at ?? j.created_at)}
        </span>
      </a>
    {:else}
      <p class="muted">No finished jobs yet.</p>
    {/each}
  {/if}
</main>

<style>
  main {
    max-width: 760px;
    margin: 2rem auto;
    padding: 0 1rem;
  }
  header {
    display: flex;
    align-items: baseline;
    gap: 0.7rem;
  }
  h1 a {
    color: inherit;
    text-decoration: none;
  }
  .version {
    font-family: ui-monospace, monospace;
    font-size: 0.8rem;
    color: #888;
    text-decoration: none;
  }
  .version:hover {
    color: #ccc;
    text-decoration: underline;
  }
  h2 {
    font-size: 0.85rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: #9a9a9a;
    margin: 1.8rem 0 0.5rem;
  }
  .row {
    display: flex;
    gap: 0.5rem;
    margin: 1rem 0;
  }
  input {
    flex: 1;
    padding: 0.55rem 0.7rem;
    background: #1e1e1e;
    color: inherit;
    border: 1px solid #333;
    border-radius: 6px;
  }
  button {
    padding: 0.55rem 1rem;
    background: #3a7;
    color: #fff;
    border: 0;
    border-radius: 6px;
    cursor: pointer;
  }
  .hit {
    display: grid;
    grid-template-columns: 1.6fr 1.4fr auto;
    gap: 0.8rem;
    align-items: center;
    padding: 0.5rem 0.8rem;
    margin-bottom: 0.3rem;
    background: #1b1b1b;
    border-radius: 6px;
  }
  .hit .muted {
    grid-column: auto;
  }
  .hit button {
    padding: 0.3rem 0.8rem;
  }
  .job {
    display: grid;
    grid-template-columns: 1fr auto;
    gap: 0.2rem 0.8rem;
    padding: 0.7rem 0.8rem;
    margin-bottom: 0.4rem;
    background: #1b1b1b;
    border-radius: 6px;
    color: inherit;
    text-decoration: none;
  }
  .job:hover {
    background: #232323;
  }
  .name {
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .meta,
  .muted {
    color: #9a9a9a;
    font-size: 0.85rem;
    grid-column: 1 / -1;
  }
  progress {
    grid-column: 1 / -1;
    width: 100%;
    height: 6px;
  }
  .chip {
    font-size: 0.75rem;
    padding: 0.1rem 0.5rem;
    border-radius: 99px;
    background: #333;
    align-self: center;
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
  .error {
    color: #cf6679;
  }
</style>
