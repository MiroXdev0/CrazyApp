def print_cluster_summary(nodes: list[str]) -> None:
    print(f"Cluster contains {len(nodes)} nodes: {', '.join(nodes)}")


if __name__ == "__main__":
    print_cluster_summary(["node-01", "node-02", "node-03"])
