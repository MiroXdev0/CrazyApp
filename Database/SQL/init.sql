CREATE TABLE IF NOT EXISTS nodes (
    id TEXT PRIMARY KEY,
    host TEXT NOT NULL,
    os TEXT NOT NULL,
    arch TEXT NOT NULL,
    cpu_cores INTEGER NOT NULL,
    ram_gb INTEGER NOT NULL,
    gpu TEXT
);

CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    command TEXT NOT NULL,
    priority INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued'
);

INSERT INTO nodes (id, host, os, arch, cpu_cores, ram_gb, gpu)
VALUES ('node-01', 'desktop-01', 'Windows', 'x64', 12, 16, 'RTX');
