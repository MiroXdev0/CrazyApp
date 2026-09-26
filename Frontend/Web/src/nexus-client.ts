export type WorkerStatus = {
  id: string;
  host: string;
  cpuCores: number;
  ramGB: number;
  gpu: string;
};

export async function fetchClusterStatus(): Promise<WorkerStatus[]> {
  return [
    { id: "node-01", host: "desktop-01", cpuCores: 12, ramGB: 16, gpu: "RTX" },
    { id: "node-02", host: "desktop-02", cpuCores: 8, ramGB: 32, gpu: "NVIDIA" },
  ];
}

export function renderClusterStatus(nodes: WorkerStatus[]) {
  return nodes.map((node) => ({
    id: node.id,
    label: `${node.host} :: ${node.cpuCores} cores / ${node.ramGB}GB`,
  }));
}
