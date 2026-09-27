import "./Style.css";
import { renderDashboard } from "./dashboard";

export function start(): void {
    const app = document.getElementById("app");

    if (!app) {
        throw new Error("Missing #app element");
    }

    renderDashboard(app);
}