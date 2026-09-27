export interface ServerStatus {
    connected: boolean;
    message: string;
}

export async function connectToServer(): Promise<ServerStatus> {
    try {
        const response = await fetch("/api/status", {
            method: "GET",
            headers: {
                Accept: "text/plain",
            },
        });

        if (!response.ok) {
            return {
                connected: false,
                message: `Gateway returned HTTP ${response.status}`,
            };
        }

        const message = (await response.text()).trim();

        return {
            connected: message.startsWith("RUNTIME_ONLINE"),
            message,
        };
    } catch (error) {
        console.error("[Nodren] Failed to connect:", error);

        return {
            connected: false,
            message: "Gateway unavailable",
        };
    }
}
