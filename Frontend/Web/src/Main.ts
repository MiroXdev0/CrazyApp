import { connectToServer } from "./ConnectToServer";

async function main() {
  const data = await connectToServer("/api/health");
  console.log("Server response:", data);
}

main().catch((error) => {
  console.error("Failed to connect:", error);
});
