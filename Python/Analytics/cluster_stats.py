def summarize_cluster(nodes: list[dict]) -> dict:
    total_cpu = sum(node.get("cpu_cores", 0) for node in nodes)
    total_ram = sum(node.get("ram_gb", 0) for node in nodes)
    return {
        "total_cpu_cores": total_cpu,
        "total_ram_gb": total_ram,
        "nodes": len(nodes),
    }


if __name__ == "__main__":
    data = [
        {"cpu_cores": 12, "ram_gb": 16},
        {"cpu_cores": 8, "ram_gb": 32},
    ]
    print(summarize_cluster(data))
