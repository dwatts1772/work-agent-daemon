<script lang="ts">
	import { onMount } from "svelte";
	import { Browser } from "@wailsio/runtime";
	import * as Table from "$lib/components/ui/table/index.js";
	import * as Alert from "$lib/components/ui/alert/index.js";
	import { Badge } from "$lib/components/ui/badge/index.js";
	import { Button } from "$lib/components/ui/button/index.js";
	import { items, onTick, run, type Command, type ItemView } from "$lib/status.js";

	let list: ItemView[] = $state([]);
	let error = $state("");
	let busy = $state("");
	let refreshedAt: Date | null = $state(null);

	async function refresh() {
		try {
			list = await items();
			refreshedAt = new Date();
		} catch (e) {
			error = message(e);
		}
	}

	async function act(command: Command, id: string) {
		busy = id;
		error = "";
		try {
			await run(command, id);
		} catch (e) {
			error = message(e);
		} finally {
			busy = "";
			await refresh();
		}
	}

	function message(e: unknown): string {
		return e instanceof Error ? e.message : String(e);
	}

	function stateVariant(item: ItemView): "default" | "secondary" | "destructive" | "outline" {
		switch (item.state) {
			case "FAILED":
				return "destructive";
			case "PAUSED":
			case "DONE":
				return "outline";
			case "READY_TO_MERGE":
				return "default";
			default:
				return "secondary";
		}
	}

	function kind(item: ItemView): string {
		return item.kind === "REVIEW_REQUEST" ? "Review Request" : "Owned Issue";
	}

	onMount(() => {
		const dark = window.matchMedia("(prefers-color-scheme: dark)");
		const theme = () => document.documentElement.classList.toggle("dark", dark.matches);
		theme();
		dark.addEventListener("change", theme);
		refresh();
		const off = onTick(refresh);
		return () => {
			off();
			dark.removeEventListener("change", theme);
		};
	});
</script>

<main class="flex min-h-screen flex-col gap-4 p-4">
	<header class="flex items-baseline justify-between">
		<h1 class="text-lg font-semibold">Work Items</h1>
		{#if refreshedAt}
			<span class="text-muted-foreground text-xs">updated {refreshedAt.toLocaleTimeString()}</span>
		{/if}
	</header>

	{#if error}
		<Alert.Root variant="destructive">
			<Alert.Title>Command failed</Alert.Title>
			<Alert.Description>{error}</Alert.Description>
		</Alert.Root>
	{/if}

	{#if list.length === 0}
		<p class="text-muted-foreground text-sm">No Work Items.</p>
	{:else}
		<Table.Root>
			<Table.Header>
				<Table.Row>
					<Table.Head>Work Item</Table.Head>
					<Table.Head>Kind</Table.Head>
					<Table.Head>State</Table.Head>
					<Table.Head>Workspace</Table.Head>
					<Table.Head>PR</Table.Head>
					<Table.Head>Held Wake</Table.Head>
					<Table.Head class="text-right">Actions</Table.Head>
				</Table.Row>
			</Table.Header>
			<Table.Body>
				{#each list as item (item.id)}
					<Table.Row>
						<Table.Cell class="max-w-56 whitespace-normal">
							<button class="font-medium hover:underline" onclick={() => Browser.OpenURL(item.url)}>{item.id}</button>
							<div class="text-muted-foreground truncate text-xs" title={item.title}>{item.title}</div>
						</Table.Cell>
						<Table.Cell>{kind(item)}</Table.Cell>
						<Table.Cell class="whitespace-normal">
							<Badge variant={stateVariant(item)}>{item.state}</Badge>
							{#if item.paused}
								<div class="text-muted-foreground text-xs">{item.paused}</div>
							{/if}
							{#if item.lastError}
								<div class="text-destructive max-w-48 truncate text-xs" title={item.lastError}>{item.lastError}</div>
							{/if}
						</Table.Cell>
						<Table.Cell class="max-w-56">
							{#if item.workspace}
								<div class="truncate font-mono text-xs" title={item.workspace}>{item.workspace}</div>
								{#if item.safeToCleanUp}
									<Badge variant="outline" class="border-green-600 text-green-700 dark:text-green-400">Safe to clean up</Badge>
								{/if}
							{:else}
								<span class="text-muted-foreground">—</span>
							{/if}
						</Table.Cell>
						<Table.Cell>
							{#if item.pr}
								<button class="hover:underline" onclick={() => Browser.OpenURL(item.prUrl)}>#{item.pr}</button>
							{:else}
								<span class="text-muted-foreground">—</span>
							{/if}
						</Table.Cell>
						<Table.Cell class="max-w-48 whitespace-normal">
							{#if item.heldWake}
								<span title={item.heldSince ? `since ${new Date(item.heldSince).toLocaleString()}` : ""}>{item.heldWake}</span>
							{:else}
								<span class="text-muted-foreground">—</span>
							{/if}
						</Table.Cell>
						<Table.Cell class="space-x-1 text-right whitespace-nowrap">
							<Button size="sm" variant="outline" disabled={!item.canPause || busy !== ""} onclick={() => act("Pause", item.id)}>Pause</Button>
							<Button size="sm" variant="outline" disabled={!item.canResume || busy !== ""} onclick={() => act("Resume", item.id)}>Resume</Button>
							<Button size="sm" disabled={!item.canWake || busy !== ""} onclick={() => act("Wake", item.id)}>Wake</Button>
						</Table.Cell>
					</Table.Row>
				{/each}
			</Table.Body>
		</Table.Root>
	{/if}
</main>
