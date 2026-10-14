<script lang="ts">
  import { api, apiKey, Unauthorized, active, ago, type Job } from "./api";
  import JobDetail from "./JobDetail.svelte";

  let jobs = $state<Job[]>([]);
  let link = $state("");
  let error = $state("");
  let needKey = $state(false);
  let keyInput = $state("");
  let selected = $state<number | null>(null);

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

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    try {
      const j = await api.submit(link.trim());
      link = "";
      error = "";
      await refresh();
      location.hash = `#/jobs/${j.id}`;
    } catch (e) {
      if (e instanceof Unauthorized) needKey = true;
      else error = (e as Error).message;
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
    c(j, "failed") + c(j, "not_stocked") + c(j, "no_match") + c(j, "ambiguous");
  const current = $derived(jobs.filter(active));
  const history = $derived(jobs.filter((j) => !active(j)));
</script>

<main>
  <header>
    <h1><a href="#/">dredge</a></h1>
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
    <form class="row" onsubmit={submit}>
      <input placeholder="Spotify playlist, album or track link" bind:value={link} required />
      <button>Download</button>
    </form>
    {#if error}<p class="error">{error}</p>{/if}

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
  h1 a {
    color: inherit;
    text-decoration: none;
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
