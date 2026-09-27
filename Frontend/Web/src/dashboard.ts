export function renderDashboard(root: HTMLElement): void {
    root.innerHTML = `
        <div class="app">
            <aside class="sidebar">
                <div class="brand">
                    <span class="brand-mark">N</span>
                    <span>Nodren</span>
                </div>

                <nav>
                    <button class="nav-item active">Overview</button>
                    <button class="nav-item">Nodes</button>
                    <button class="nav-item">Workloads</button>
                    <button class="nav-item">Network</button>
                    <button class="nav-item">Settings</button>
                </nav>
            </aside>

            <main class="content">
                <header class="topbar">
                    <div>
                        <h1>Overview</h1>
                        <p>Nodren ecosystem control center</p>
                    </div>

                    <div class="connection">
                        <span class="status-dot"></span>
                        <span>Disconnected</span>
                    </div>
                </header>

                <section class="stats">
                    <article class="card">
                        <span>Nodes</span>
                        <strong>0</strong>
                    </article>

                    <article class="card">
                        <span>Active workloads</span>
                        <strong>0</strong>
                    </article>

                    <article class="card">
                        <span>CPU usage</span>
                        <strong>0%</strong>
                    </article>

                    <article class="card">
                        <span>Memory</span>
                        <strong>0 GB</strong>
                    </article>
                </section>

                <section class="panel">
                    <div class="panel-header">
                        <div>
                            <h2>System</h2>
                            <p>Nodren server connection</p>
                        </div>

                        <button id="connect-button">
                            Connect
                        </button>
                    </div>

                    <div id="system-status">
                        Waiting for connection...
                    </div>
                </section>
            </main>
        </div>
    `;
}