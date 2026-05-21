#!/usr/bin/env python3
"""
Phase 4 Slice 10.0: DAG Visualization HTML Generator

Reads DAG state from Redis and generates an interactive HTML dashboard.
Usage: python scripts/render_dag_visual_html.py <workflow_id> [--redis-host localhost] [--redis-port 6379] [--output dag_visual.html]
"""

import argparse
import json
import sys
import os
from datetime import datetime

try:
    import redis
except ImportError:
    print("Error: redis package not installed. Run: pip install redis")
    sys.exit(1)


# Color scheme for node status
STATUS_COLORS = {
    "pending": "#FFC107",    # Yellow
    "running": "#2196F3",    # Blue
    "completed": "#4CAF50",  # Green
    "failed": "#F44336",     # Red
}

STATUS_LABELS = {
    "pending": "⏳ Pending",
    "running": "🔄 Running",
    "completed": "✅ Completed",
    "failed": "❌ Failed",
}


def parse_timestamp(ns: int) -> str:
    """Convert nanoseconds timestamp to readable format."""
    if ns <= 0:
        return "-"
    try:
        ts = ns / 1e9
        dt = datetime.fromtimestamp(ts)
        return dt.strftime("%H:%M:%S.%f")[:-3]
    except:
        return str(ns)


def parse_duration(start_ns: int, end_ns: int) -> str:
    """Calculate duration between two timestamps in milliseconds."""
    if start_ns <= 0 or end_ns <= 0:
        return "-"
    duration_ms = (end_ns - start_ns) / 1e6
    return f"{duration_ms:.1f}ms"


def get_dag_state(redis_client, workflow_id: str) -> dict:
    """Get DAG state from Redis."""
    meta_key = f"dag:{workflow_id}:meta"
    nodes_key = f"dag:{workflow_id}:nodes"

    # Get meta
    meta = {}
    try:
        meta_data = redis_client.hgetall(meta_key)
        for k, v in meta_data.items():
            meta[k.decode() if isinstance(k, bytes) else k] = v.decode() if isinstance(v, bytes) else v
    except Exception as e:
        print(f"Warning: Failed to get meta: {e}")

    # Get nodes
    nodes = {}
    try:
        nodes_data = redis_client.hgetall(nodes_key)
        for k, v in nodes_data.items():
            node_key = k.decode() if isinstance(k, bytes) else k
            node_val = v.decode() if isinstance(v, bytes) else v
            try:
                nodes[node_key] = json.loads(node_val)
            except json.JSONDecodeError:
                nodes[node_key] = {"raw": node_val}
    except Exception as e:
        print(f"Warning: Failed to get nodes: {e}")

    return {"meta": meta, "nodes": nodes}


def group_nodes_by_layer(nodes: dict) -> dict[int, list]:
    """Group nodes by their layer."""
    layers = {}
    for node_id, node_data in nodes.items():
        layer = node_data.get("layer", 0)
        if layer not in layers:
            layers[layer] = []
        layers[layer].append({
            "node_id": node_id,
            "data": node_data
        })
    # Sort each layer by node_id
    for layer in layers:
        layers[layer].sort(key=lambda x: x["node_id"])
    return dict(sorted(layers.items()))


def generate_mermaid_graph(nodes: dict) -> str:
    """Generate Mermaid graph definition from nodes."""
    lines = ["graph TD"]
    node_defs = set()
    edges = []

    for node_id, node_data in nodes.items():
        status = node_data.get("status", "unknown")
        color = STATUS_COLORS.get(status, "#9E9E9E")

        # Create node definition with HTML-style label
        deps = node_data.get("dependencies", [])
        deps_str = ", ".join(deps) if deps else ""
        layer = node_data.get("layer", 0)
        label = f"{node_id}<br/><small>{status}</small>"
        if deps_str:
            label += f"<br/><small style='color:#888'>deps: {deps_str}</small>"

        # Mermaid doesn't support HTML in node labels well, use simple labels
        node_defs.add(f'    {node_id}["{node_id}"]')

        # Add edges based on dependencies
        for dep in deps:
            if dep in nodes:
                edges.append(f'    {dep} --> {node_id}')

    # Build graph definition
    result = []
    for node_def in sorted(node_defs):
        result.append(node_def)

    # Only add edges if we have dependencies
    if edges:
        for edge in sorted(set(edges)):
            result.append(edge)
    else:
        result.append('    subgraph "No dependencies defined"')
        result.append('    end')

    return "\n".join(result)


def generate_svg_graph(nodes: dict) -> str:
    """Generate SVG graph from nodes."""
    if not nodes:
        return '<text x="200" y="100" fill="#666">No nodes to display</text>'

    # Group nodes by layer
    layers = group_nodes_by_layer(nodes)

    # Calculate positions
    node_width = 140
    node_height = 60
    layer_spacing = 180
    node_spacing = 100
    margin_left = 50
    margin_top = 50

    # Calculate SVG dimensions
    max_layer = max(layers.keys()) if layers else 0
    nodes_in_layer = max(len(layer_nodes) for layer_nodes in layers.values()) if layers else 1

    width = margin_left + (max_layer + 1) * layer_spacing + 100
    height = margin_top + nodes_in_layer * node_spacing + 100

    svg_parts = [f'<svg width="{width}" height="{height}" xmlns="http://www.w3.org/2000/svg">']

    # Add arrow marker definition
    svg_parts.append('''    <defs>
        <marker id="arrowhead" markerWidth="10" markerHeight="7" refX="9" refY="3.5" orient="auto">
            <polygon points="0 0, 10 3.5, 0 7" fill="#888"/>
        </marker>
    </defs>''')

    # Calculate positions for each node
    node_positions = {}
    for layer, layer_nodes in layers.items():
        layer_height = len(layer_nodes) * node_spacing
        start_y = (height - layer_height) / 2

        for i, node_info in enumerate(layer_nodes):
            node_id = node_info["node_id"]
            x = margin_left + layer * layer_spacing
            y = start_y + i * node_spacing + node_height / 2
            node_positions[node_id] = (x, y)

    # Draw edges first (so they appear behind nodes)
    drawn_edges = set()
    for node_id, node_data in nodes.items():
        deps = node_data.get("dependencies", [])
        status = node_data.get("status", "unknown")
        color = STATUS_COLORS.get(status, "#9E9E9E")

        x, y = node_positions.get(node_id, (0, 0))
        target_x = x + node_width
        target_y = y

        for dep in deps:
            if dep in node_positions and (dep, node_id) not in drawn_edges:
                dep_x, dep_y = node_positions[dep]
                drawn_edges.add((dep, node_id))

                # Draw curved arrow
                mid_x = (dep_x + node_width + target_x) / 2
                svg_parts.append(
                    f'    <path d="M {dep_x + node_width},{dep_y} '
                    f'C {mid_x},{dep_y} {mid_x},{target_y} {target_x},{target_y}" '
                    f'stroke="#888" stroke-width="2" fill="none" marker-end="url(#arrowhead)"/>'
                )

    # Draw nodes
    for node_id, node_data in nodes.items():
        status = node_data.get("status", "unknown")
        color = STATUS_COLORS.get(status, "#9E9E9E")
        deps = node_data.get("dependencies", [])
        layer = node_data.get("layer", 0)

        x, y = node_positions.get(node_id, (0, 0))

        # Node box
        svg_parts.append(
            f'    <rect x="{x}" y="{y - node_height/2}" width="{node_width}" height="{node_height}" '
            f'rx="8" fill="{color}20" stroke="{color}" stroke-width="2"/>'
        )

        # Node label
        svg_parts.append(
            f'    <text x="{x + node_width/2}" y="{y - 5}" text-anchor="middle" '
            f'font-family="Segoe UI,sans-serif" font-size="14" font-weight="bold" fill="#eee">{node_id}</text>'
        )

        # Status label
        svg_parts.append(
            f'    <text x="{x + node_width/2}" y="{y + 15}" text-anchor="middle" '
            f'font-family="Segoe UI,sans-serif" font-size="11" fill="{color}">{status}</text>'
        )

        # Layer indicator
        svg_parts.append(
            f'    <text x="{x + node_width/2}" y="{y + node_height/2 - 5}" text-anchor="middle" '
            f'font-family="Segoe UI,sans-serif" font-size="9" fill="#888">Layer {layer}</text>'
        )

    svg_parts.append('</svg>')
    return "\n".join(svg_parts)


def generate_html(workflow_id: str, dag_state: dict) -> str:
    """Generate HTML dashboard."""
    meta = dag_state.get("meta", {})
    nodes = dag_state.get("nodes", {})
    layers = group_nodes_by_layer(nodes)

    # Build status summary
    total_nodes = int(meta.get("total_nodes", len(nodes)))
    pending = int(meta.get("pending_nodes", 0))
    running = int(meta.get("running_nodes", 0))
    completed = int(meta.get("completed_nodes", 0))
    failed = int(meta.get("failed_nodes", 0))
    updated_at = parse_timestamp(int(meta.get("updated_at_ns", 0)))

    # Generate node rows
    node_rows = []
    for layer, layer_nodes in layers.items():
        for node_info in layer_nodes:
            node_id = node_info["node_id"]
            node = node_info["data"]
            status = node.get("status", "unknown")
            color = STATUS_COLORS.get(status, "#9E9E9E")
            label = STATUS_LABELS.get(status, status.upper())
            deps = node.get("dependencies", [])
            deps_str = ", ".join(deps) if deps else "-"
            started = parse_timestamp(int(node.get("started_at_ns", 0)))
            completed_ts = parse_timestamp(int(node.get("completed_at_ns", 0)))
            duration = parse_duration(
                int(node.get("started_at_ns", 0)),
                int(node.get("completed_at_ns", 0))
            )
            error = node.get("error", "")

            node_rows.append(f"""
            <tr class="node-row" data-status="{status}">
                <td class="node-id">{node_id}</td>
                <td class="node-status" style="background-color: {color}20; border-left: 4px solid {color};">
                    <span class="status-badge" style="background-color: {color};">{label}</span>
                </td>
                <td class="node-layer">Layer {layer}</td>
                <td class="node-deps">{deps_str}</td>
                <td class="node-time">{started}</td>
                <td class="node-time">{completed_ts}</td>
                <td class="node-duration">{duration}</td>
                <td class="node-error">{"<span class='error-text'>" + error + "</span>" if error else "-"}</td>
            </tr>
            """)

    nodes_html = "\n".join(node_rows) if node_rows else "<tr><td colspan='8' class='no-data'>No nodes found</td></tr>"

    # Generate SVG graph
    svg_graph = generate_svg_graph(nodes)

    html = f"""<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>DAG Visualization - {workflow_id}</title>
    <style>
        * {{ box-sizing: border-box; margin: 0; padding: 0; }}
        body {{ font-family: 'Segoe UI', system-ui, sans-serif; background: #1a1a2e; color: #eee; min-height: 100vh; padding: 20px; }}
        .container {{ max-width: 1200px; margin: 0 auto; }}
        h1 {{ color: #fff; margin-bottom: 20px; font-size: 1.5rem; }}
        .workflow-id {{ font-family: monospace; background: #16213e; padding: 2px 8px; border-radius: 4px; }}

        /* Summary Cards */
        .summary {{ display: flex; gap: 15px; margin-bottom: 25px; flex-wrap: wrap; }}
        .summary-card {{ background: #16213e; padding: 15px 20px; border-radius: 8px; min-width: 120px; }}
        .summary-card .label {{ font-size: 0.75rem; color: #888; text-transform: uppercase; margin-bottom: 5px; }}
        .summary-card .value {{ font-size: 1.5rem; font-weight: bold; }}
        .summary-card.total {{ border-left: 4px solid #9E9E9E; }}
        .summary-card.pending {{ border-left: 4px solid {STATUS_COLORS['pending']}; }}
        .summary-card.running {{ border-left: 4px solid {STATUS_COLORS['running']}; }}
        .summary-card.completed {{ border-left: 4px solid {STATUS_COLORS['completed']}; }}
        .summary-card.failed {{ border-left: 4px solid {STATUS_COLORS['failed']}; }}

        /* Status Legend */
        .legend {{ display: flex; gap: 20px; margin-bottom: 20px; flex-wrap: wrap; }}
        .legend-item {{ display: flex; align-items: center; gap: 6px; font-size: 0.85rem; }}
        .legend-dot {{ width: 12px; height: 12px; border-radius: 3px; }}

        /* Table */
        .table-container {{ background: #16213e; border-radius: 12px; overflow: hidden; }}
        table {{ width: 100%; border-collapse: collapse; }}
        th {{ background: #0f3460; padding: 12px 15px; text-align: left; font-weight: 600; font-size: 0.85rem; color: #aaa; }}
        td {{ padding: 12px 15px; border-bottom: 1px solid #1a1a2e; font-size: 0.9rem; }}
        tr:last-child td {{ border-bottom: none; }}
        tr:hover {{ background: #1a2744; }}

        .node-id {{ font-family: monospace; color: #64b5f6; }}
        .node-status {{ min-width: 120px; }}
        .status-badge {{ padding: 4px 10px; border-radius: 12px; font-size: 0.75rem; font-weight: 600; color: #fff; }}
        .node-layer {{ color: #888; }}
        .node-deps {{ font-size: 0.8rem; color: #888; }}
        .node-time {{ font-family: monospace; font-size: 0.8rem; color: #aaa; }}
        .node-duration {{ font-family: monospace; font-size: 0.85rem; color: #4CAF50; }}
        .node-error {{ max-width: 200px; }}
        .error-text {{ color: #F44336; font-size: 0.8rem; }}

        .no-data {{ text-align: center; color: #666; padding: 30px; }}

        .footer {{ margin-top: 20px; text-align: center; color: #666; font-size: 0.75rem; }}

        /* SVG Graph */
        .graph-container {{ background: #16213e; border-radius: 12px; padding: 20px; margin-bottom: 25px; overflow-x: auto; }}
        .graph-container h2 {{ color: #fff; margin-bottom: 15px; font-size: 1rem; }}
        .graph-svg {{ display: block; margin: 0 auto; }}
    </style>
</head>
<body>
    <div class="container">
        <h1>DAG Visualization Dashboard <span class="workflow-id">{workflow_id}</span></h1>

        <div class="summary">
            <div class="summary-card total">
                <div class="label">Total Nodes</div>
                <div class="value">{total_nodes}</div>
            </div>
            <div class="summary-card pending">
                <div class="label">Pending</div>
                <div class="value">{pending}</div>
            </div>
            <div class="summary-card running">
                <div class="label">Running</div>
                <div class="value">{running}</div>
            </div>
            <div class="summary-card completed">
                <div class="label">Completed</div>
                <div class="value">{completed}</div>
            </div>
            <div class="summary-card failed">
                <div class="label">Failed</div>
                <div class="value">{failed}</div>
            </div>
        </div>

        <div class="legend">
            <div class="legend-item"><div class="legend-dot" style="background: {STATUS_COLORS['pending']}"></div> Pending</div>
            <div class="legend-item"><div class="legend-dot" style="background: {STATUS_COLORS['running']}"></div> Running</div>
            <div class="legend-item"><div class="legend-dot" style="background: {STATUS_COLORS['completed']}"></div> Completed</div>
            <div class="legend-item"><div class="legend-dot" style="background: {STATUS_COLORS['failed']}"></div> Failed</div>
        </div>

        <div class="graph-container">
            <h2>DAG Graph (Dependencies)</h2>
            <div class="graph-svg">
                {svg_graph}
            </div>
        </div>

        <div class="table-container">
            <table>
                <thead>
                    <tr>
                        <th>Node ID</th>
                        <th>Status</th>
                        <th>Layer</th>
                        <th>Dependencies</th>
                        <th>Started</th>
                        <th>Completed</th>
                        <th>Duration</th>
                        <th>Error</th>
                    </tr>
                </thead>
                <tbody>
                    {nodes_html}
                </tbody>
            </table>
        </div>

        <div class="footer">
            Generated at {datetime.now().strftime('%Y-%m-%d %H:%M:%S')} | Phase 4 Slice 10.0 DAG Visualization
        </div>
    </div>
</body>
</html>"""

    return html


def main():
    parser = argparse.ArgumentParser(
        description="Generate DAG Visualization HTML Dashboard from Redis data",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  python scripts/render_dag_visual_html.py task-abc-123
  python scripts/render_dag_visual_html.py task-abc-123 --output my_dag.html
  python scripts/render_dag_visual_html.py task-abc-123 --redis-host localhost --redis-port 6379
        """
    )
    parser.add_argument("workflow_id", help="DAG workflow ID (e.g., task-abc-123)")
    parser.add_argument("--output", "-o", default="dag_visual.html", help="Output HTML file (default: dag_visual.html)")
    parser.add_argument("--redis-host", default=os.environ.get("REDIS_HOST", "localhost"), help="Redis host")
    parser.add_argument("--redis-port", type=int, default=int(os.environ.get("REDIS_PORT", "6379")), help="Redis port")
    parser.add_argument("--redis-db", type=int, default=0, help="Redis database number")
    parser.add_argument("--redis-pass", default=os.environ.get("REDIS_PASS", ""), help="Redis password")

    args = parser.parse_args()

    # Connect to Redis
    try:
        client = redis.Redis(
            host=args.redis_host,
            port=args.redis_port,
            db=args.redis_db,
            password=args.redis_pass if args.redis_pass else None,
            decode_responses=True,
            socket_connect_timeout=5
        )
        client.ping()
        print(f"Connected to Redis at {args.redis_host}:{args.redis_port}")
    except Exception as e:
        print(f"Error: Failed to connect to Redis: {e}")
        sys.exit(1)

    # Get DAG state
    print(f"Fetching DAG state for workflow: {args.workflow_id}")
    dag_state = get_dag_state(client, args.workflow_id)

    if not dag_state.get("nodes"):
        print("Warning: No DAG nodes found for this workflow_id")
        print("The workflow may not have completed yet, or the data has expired (TTL: 1 day)")

    # Generate HTML
    html = generate_html(args.workflow_id, dag_state)

    # Write to file
    with open(args.output, "w", encoding="utf-8") as f:
        f.write(html)

    print(f"Dashboard saved to: {args.output}")
    print(f"Open in browser: file://{os.path.abspath(args.output)}")

    # Offer to open in browser
    try:
        import webbrowser
        webbrowser.open(f"file://{os.path.abspath(args.output)}")
    except:
        pass


if __name__ == "__main__":
    main()