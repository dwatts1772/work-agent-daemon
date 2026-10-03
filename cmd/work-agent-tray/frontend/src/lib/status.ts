// The Go status-window service (cmd/work-agent-tray/window.go), called by
// name through the Wails runtime.
import { Call, Events } from "@wailsio/runtime";

/** One Work Item as core.View shows it. */
export interface ItemView {
	id: string;
	kind: string;
	state: string;
	title: string;
	url: string;
	workspace: string;
	branch: string;
	pr: number;
	prUrl: string;
	paused: string;
	heldWake: string;
	heldSince: string | null;
	lastError: string;
	safeToCleanUp: boolean;
	canPause: boolean;
	canResume: boolean;
	canWake: boolean;
}

export type Command = "Pause" | "Resume" | "Wake";

const service = "main.statusWindow";

export async function items(): Promise<ItemView[]> {
	return (await Call.ByName(`${service}.Items`)) ?? [];
}

/** Runs the same command as `work-agent pause|resume|wake <id>`. */
export function run(command: Command, id: string): Promise<ItemView> {
	return Call.ByName(`${service}.${command}`, id);
}

/** Calls refresh after every Tick; returns the unsubscribe function. */
export function onTick(refresh: () => void): () => void {
	return Events.On("work-agent:tick", refresh);
}
