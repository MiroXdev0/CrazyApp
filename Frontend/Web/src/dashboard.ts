export type NodeSummary = {
  id: string;
  host: string;
  cpuCores: number;
  ramGB: number;
  health: string;
};

export function buildDashboard(nodes: NodeSummary[]) {
  return {
    totalNodes: nodes.length,
    totalCpu: nodes.reduce((sum, node) => sum + node.cpuCores, 0),
    totalRam: nodes.reduce((sum, node) => sum + node.ramGB, 0),
    nodes,
  };
}

const sample: NodeSummary[] = [
  { id: "node-01", host: "desktop-01", cpuCores: 12, ramGB: 16, health: "online" },
  { id: "node-02", host: "desktop-02", cpuCores: 8, ramGB: 32, health: "online" },
];

console.log(buildDashboard(sample));
