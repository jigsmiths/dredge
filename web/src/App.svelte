<script lang="ts">
  let status = $state<string | null>(null);
  let error = $state<string | null>(null);

  $effect(() => {
    fetch("/health")
      .then((r) => r.json())
      .then((data) => (status = data.status))
      .catch((e) => (error = (e as Error).message));
  });
</script>

<main>
  <h1>APP_NAME</h1>
  {#if error}
    <p class="error">Backend unreachable: {error}</p>
  {:else if status}
    <p>Backend status: {status}</p>
  {:else}
    <p>Checking backend...</p>
  {/if}
</main>

<style>
  main {
    max-width: 640px;
    margin: 4rem auto;
    padding: 0 1rem;
    font-family: Inter, system-ui, Avenir, Helvetica, Arial, sans-serif;
  }

  .error {
    color: #cf6679;
  }
</style>
