from dataclasses import dataclass, field
from typing import Dict


@dataclass
class Node:
    id: str
    host: str
    os: str
    arch: str
    cpu_cores: int
    ram_gb: int
    gpu: str
    online: bool = True


class NodeRegistry:
    def __init__(self):
        self.nodes: Dict[str, Node] = {}

    def register(self, node: Node) -> None:
        self.nodes[node.id] = node

    def list_nodes(self) -> list[str]:
        return list(self.nodes.keys())

    def count(self) -> int:
        return len(self.nodes)


if __name__ == "__main__":
    registry = NodeRegistry()
    registry.register(Node("node-01", "desktop-01", "Windows", "x64", 12, 16, "RTX"))
    print(registry.list_nodes())
