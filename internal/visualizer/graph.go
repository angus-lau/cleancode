package visualizer

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/angus-lau/cleancode/internal/indexer"
)

type fileNode struct {
	ID      string       `json:"id"`
	Label   string       `json:"label"`
	Group   string       `json:"group"`
	Dir     string       `json:"dir"`
	Symbols []symbolInfo `json:"symbols"`
	Edges   int          `json:"edgeCount"`
}

type symbolInfo struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Line int    `json:"line"`
}

type fileEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Weight int    `json:"weight"`
}

type graphData struct {
	Nodes []fileNode `json:"nodes"`
	Edges []fileEdge `json:"edges"`
}

// GenerateHTML creates an interactive file-level dependency graph and opens it in the browser.
// If focus is non-empty, only show files within a few hops of the focus file/path.
func GenerateHTML(symbols []indexer.Symbol, edges []indexer.Edge, rootPath string, focus string) error {
	data := buildFileGraph(symbols, edges, rootPath, focus)

	dataJSON, err := json.Marshal(data)
	if err != nil {
		return err
	}

	html := fmt.Sprintf(htmlTemplate, string(dataJSON))

	outPath := filepath.Join(os.TempDir(), "cleancode-graph.html")
	if err := os.WriteFile(outPath, []byte(html), 0644); err != nil {
		return err
	}

	fmt.Printf("Graph written to %s\n", outPath)
	return openBrowser(outPath)
}

func buildFileGraph(symbols []indexer.Symbol, edges []indexer.Edge, rootPath string, focus string) graphData {
	// Map symbol IDs to their file paths
	symToFile := make(map[string]string)
	fileSymbols := make(map[string][]symbolInfo)

	for _, sym := range symbols {
		id := fmt.Sprintf("%s:%s:%d", sym.FilePath, sym.Name, sym.StartLine)
		relPath := sym.FilePath
		if strings.HasPrefix(relPath, rootPath) {
			relPath = strings.TrimPrefix(relPath, rootPath+"/")
		}
		symToFile[id] = relPath
		fileSymbols[relPath] = append(fileSymbols[relPath], symbolInfo{
			Name: sym.Name,
			Kind: string(sym.Kind),
			Line: sym.StartLine,
		})
	}

	// Aggregate symbol edges into file-level edges
	type edgeKey struct{ from, to string }
	fileEdgeWeights := make(map[edgeKey]int)
	fileEdgeCount := make(map[string]int)

	for _, e := range edges {
		fromFile := symToFile[e.From]
		toFile := symToFile[e.To]
		if fromFile == "" || toFile == "" || fromFile == toFile {
			continue
		}
		key := edgeKey{fromFile, toFile}
		fileEdgeWeights[key]++
		fileEdgeCount[fromFile]++
		fileEdgeCount[toFile]++
	}

	// Determine which files to include
	includedFiles := make(map[string]bool)

	if focus != "" {
		// Find the focus file (match by substring)
		focusLower := strings.ToLower(focus)
		var focusFile string
		for f := range fileSymbols {
			if strings.ToLower(f) == focusLower || strings.Contains(strings.ToLower(f), focusLower) {
				focusFile = f
				break
			}
		}
		if focusFile == "" {
			// Try matching by symbol name
			for _, sym := range symbols {
				if strings.EqualFold(sym.Name, focus) || strings.Contains(strings.ToLower(sym.Name), focusLower) {
					rel := sym.FilePath
					if strings.HasPrefix(rel, rootPath) {
						rel = strings.TrimPrefix(rel, rootPath+"/")
					}
					focusFile = rel
					break
				}
			}
		}

		if focusFile != "" {
			// Include focus file + 2 hops
			includedFiles[focusFile] = true
			// Hop 1
			hop1 := make(map[string]bool)
			for key := range fileEdgeWeights {
				if key.from == focusFile {
					hop1[key.to] = true
					includedFiles[key.to] = true
				}
				if key.to == focusFile {
					hop1[key.from] = true
					includedFiles[key.from] = true
				}
			}
			// Hop 2
			for f := range hop1 {
				for key := range fileEdgeWeights {
					if key.from == f {
						includedFiles[key.to] = true
					}
					if key.to == f {
						includedFiles[key.from] = true
					}
				}
			}
		}
	} else {
		// No focus: include all files that have edges
		for key := range fileEdgeWeights {
			includedFiles[key.from] = true
			includedFiles[key.to] = true
		}
	}

	// Build nodes — group by top-level directory (depth 2, e.g. src/services)
	var nodes []fileNode
	nodeSet := make(map[string]bool)
	for f := range includedFiles {
		ext := filepath.Ext(f)
		dir := filepath.Dir(f)
		if dir == "." {
			dir = "root"
		}
		// Truncate to depth 2 for clustering (e.g. src/controller/feed/services -> src/controller)
		parts := strings.Split(dir, "/")
		clusterDir := dir
		if len(parts) > 2 {
			clusterDir = parts[0] + "/" + parts[1]
		}
		nodes = append(nodes, fileNode{
			ID:      f,
			Label:   filepath.Base(f),
			Group:   ext,
			Dir:     clusterDir,
			Symbols: fileSymbols[f],
			Edges:   fileEdgeCount[f],
		})
		nodeSet[f] = true
	}

	// Build edges
	var resultEdges []fileEdge
	for key, weight := range fileEdgeWeights {
		if nodeSet[key.from] && nodeSet[key.to] {
			resultEdges = append(resultEdges, fileEdge{
				Source: key.from,
				Target: key.to,
				Weight: weight,
			})
		}
	}

	return graphData{Nodes: nodes, Edges: resultEdges}
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return fmt.Errorf("unsupported platform")
	}
	return cmd.Start()
}

const htmlTemplate = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>cleancode — dependency graph</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { background: #0d1117; color: #c9d1d9; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', monospace; overflow: hidden; }

  #controls {
    position: fixed; top: 0; left: 0; right: 0; z-index: 10;
    padding: 12px 20px; background: #161b22; border-bottom: 1px solid #30363d;
    display: flex; align-items: center; gap: 16px;
  }
  #controls h1 { font-size: 14px; font-weight: 600; color: #58a6ff; white-space: nowrap; }
  #search {
    background: #0d1117; border: 1px solid #30363d; color: #c9d1d9;
    padding: 6px 12px; border-radius: 6px; font-size: 13px; width: 300px; outline: none;
  }
  #search:focus { border-color: #58a6ff; }
  #stats { font-size: 12px; color: #8b949e; }
  .hint { font-size: 11px; color: #484f58; }
  #legend { display: flex; gap: 12px; margin-left: auto; font-size: 12px; }
  .legend-item { display: flex; align-items: center; gap: 4px; }
  .legend-dot { width: 10px; height: 10px; border-radius: 50%%; }

  #tooltip {
    position: fixed; padding: 10px 14px; background: #1c2128; border: 1px solid #30363d;
    border-radius: 8px; font-size: 12px; pointer-events: none; display: none;
    max-width: 350px; z-index: 20; line-height: 1.5;
  }
  #tooltip .path { color: #7ee787; font-weight: 600; font-size: 13px; }
  #tooltip .dir { color: #8b949e; font-size: 11px; }
  #tooltip .sym-list { margin-top: 6px; max-height: 200px; overflow-y: auto; }
  #tooltip .sym { color: #c9d1d9; }
  #tooltip .sym-kind { color: #484f58; font-size: 11px; }

  #sidebar {
    position: fixed; top: 50px; right: 0; width: 320px; bottom: 0;
    background: #161b22; border-left: 1px solid #30363d; padding: 16px;
    overflow-y: auto; display: none; z-index: 10;
  }
  #sidebar h2 { font-size: 13px; color: #58a6ff; margin-bottom: 4px; }
  #sidebar .path { font-size: 12px; color: #7ee787; margin-bottom: 12px; }
  #sidebar .sym-item {
    padding: 4px 0; border-bottom: 1px solid #21262d; font-size: 12px;
    display: flex; justify-content: space-between;
  }
  #sidebar .sym-name { color: #c9d1d9; }
  #sidebar .sym-meta { color: #484f58; }
  #sidebar .close-btn {
    position: absolute; top: 12px; right: 12px; background: none; border: none;
    color: #8b949e; cursor: pointer; font-size: 16px;
  }
  #sidebar .section { margin-top: 16px; }
  #sidebar .section h3 { font-size: 12px; color: #8b949e; margin-bottom: 6px; }
  #sidebar .conn-item { font-size: 12px; color: #79c0ff; padding: 2px 0; cursor: pointer; }
  #sidebar .conn-item:hover { color: #58a6ff; text-decoration: underline; }

  svg { position: fixed; top: 50px; left: 0; }
</style>
</head>
<body>
<div id="controls">
  <h1>cleancode</h1>
  <input id="search" type="text" placeholder="Search files..." autocomplete="off" />
  <span id="stats"></span>
  <span class="hint">click file = details &nbsp;|&nbsp; click bg = reset</span>
  <div id="legend"></div>
</div>
<div id="tooltip"></div>
<div id="sidebar">
  <button class="close-btn" onclick="closeSidebar()">x</button>
  <h2 id="sb-file"></h2>
  <div class="path" id="sb-path"></div>
  <div id="sb-symbols"></div>
  <div class="section" id="sb-imports-section"><h3>Imports from</h3><div id="sb-imports"></div></div>
  <div class="section" id="sb-importedby-section"><h3>Imported by</h3><div id="sb-importedby"></div></div>
</div>
<svg></svg>

<script src="https://d3js.org/d3.v7.min.js"></script>
<script>
const data = %s;

const defaultColor = '#8b949e';

const width = window.innerWidth;
const height = window.innerHeight - 50;

const svg = d3.select('svg').attr('width', width).attr('height', height);
const g = svg.append('g');

const zoom = d3.zoom().scaleExtent([0.1, 8]).on('zoom', (e) => g.attr('transform', e.transform));
svg.call(zoom);

// No arrowheads — they clutter the view at scale

// Size nodes by edge count
const maxEdges = Math.max(1, ...data.nodes.map(n => n.edgeCount));
const rScale = d3.scaleSqrt().domain([0, maxEdges]).range([6, 22]);

// Directory clustering: group files by directory
const dirs = [...new Set(data.nodes.map(n => n.dir))];
const dirIndex = {};
dirs.forEach((d, i) => { dirIndex[d] = i; });

// Color nodes by directory
const dirColors = {};
const dirPalette = ['#58a6ff','#f97583','#7ee787','#d2a8ff','#f0883e','#56d364','#f778ba','#79c0ff','#ffa657','#ff7b72'];
dirs.forEach((d, i) => { dirColors[d] = dirPalette[i %% dirPalette.length]; });

// Create cluster centers arranged in a circle
const clusterCenters = {};
const clusterRadius = Math.min(width, height) * 0.35;
dirs.forEach((d, i) => {
  const angle = (2 * Math.PI * i) / dirs.length;
  clusterCenters[d] = {
    x: width / 2 + clusterRadius * Math.cos(angle),
    y: height / 2 + clusterRadius * Math.sin(angle),
  };
});

const simulation = d3.forceSimulation(data.nodes)
  .force('link', d3.forceLink(data.edges).id(d => d.id).distance(80))
  .force('charge', d3.forceManyBody().strength(-150))
  .force('center', d3.forceCenter(width / 2, height / 2).strength(0.02))
  .force('collision', d3.forceCollide(d => rScale(d.edgeCount) + 4))
  .force('clusterX', d3.forceX(d => clusterCenters[d.dir].x).strength(0.3))
  .force('clusterY', d3.forceY(d => clusterCenters[d.dir].y).strength(0.3));

const link = g.append('g')
  .selectAll('line').data(data.edges).join('line')
  .attr('stroke', '#21262d')
  .attr('stroke-width', d => Math.max(0.5, Math.min(d.weight * 0.5, 2)))
  .attr('stroke-opacity', 0.4);

const node = g.append('g')
  .selectAll('circle').data(data.nodes).join('circle')
  .attr('r', d => rScale(d.edgeCount))
  .attr('fill', d => dirColors[d.dir] || defaultColor)
  .attr('fill-opacity', 0.85)
  .attr('stroke', '#0d1117').attr('stroke-width', 2)
  .style('cursor', 'pointer')
  .call(d3.drag()
    .on('start', (e, d) => { if (!e.active) simulation.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; })
    .on('drag', (e, d) => { d.fx = e.x; d.fy = e.y; })
    .on('end', (e, d) => { if (!e.active) simulation.alphaTarget(0); d.fx = null; d.fy = null; })
  );

const label = g.append('g')
  .selectAll('text').data(data.nodes).join('text')
  .text(d => d.label)
  .attr('font-size', d => rScale(d.edgeCount) > 12 ? 11 : 9)
  .attr('fill', '#8b949e')
  .attr('dx', d => rScale(d.edgeCount) + 4).attr('dy', 3)
  .style('pointer-events', 'none');

// Directory label layer (behind nodes)
const dirLabelG = g.insert('g', ':first-child');

// Convex hull backgrounds for directory clusters
const hullG = g.insert('g', ':first-child');

function updateHulls() {
  // Group node positions by directory
  const dirPoints = {};
  data.nodes.forEach(n => {
    if (n.x == null || n.y == null) return;
    if (!dirPoints[n.dir]) dirPoints[n.dir] = [];
    dirPoints[n.dir].push([n.x, n.y]);
  });

  const hullData = [];
  for (const dir in dirPoints) {
    const pts = dirPoints[dir];
    if (pts.length < 3) continue;
    const hull = d3.polygonHull(pts);
    if (hull) hullData.push({ dir, hull });
  }

  const hullLine = d3.line().curve(d3.curveCatmullRomClosed.alpha(1));
  const hulls = hullG.selectAll('path').data(hullData, d => d.dir);
  hulls.enter().append('path')
    .attr('fill', d => dirColors[d.dir])
    .attr('fill-opacity', 0)
    .attr('stroke', '#30363d')
    .attr('stroke-opacity', 0.2)
    .attr('stroke-width', 1)
    .attr('stroke-dasharray', '4,3')
    .merge(hulls)
    .attr('d', d => {
      // Expand hull outward by padding
      const pad = 28;
      const cx = d3.mean(d.hull, p => p[0]);
      const cy = d3.mean(d.hull, p => p[1]);
      const expanded = d.hull.map(p => {
        const dx = p[0] - cx, dy = p[1] - cy;
        const dist = Math.sqrt(dx*dx + dy*dy) || 1;
        return [p[0] + (dx/dist)*pad, p[1] + (dy/dist)*pad];
      });
      return hullLine(expanded);
    });
  hulls.exit().remove();

  // Update directory labels (positioned at cluster centroid)
  const labelData = [];
  for (const dir in dirPoints) {
    if (dirPoints[dir].length < 2) continue;
    labelData.push({
      dir,
      x: d3.mean(dirPoints[dir], p => p[0]),
      y: d3.min(dirPoints[dir], p => p[1]) - 18,
    });
  }
  const dLabels = dirLabelG.selectAll('text').data(labelData, d => d.dir);
  dLabels.enter().append('text')
    .attr('font-size', 11)
    .attr('fill', d => dirColors[d.dir])
    .attr('fill-opacity', 0.5)
    .attr('text-anchor', 'middle')
    .attr('font-weight', 600)
    .style('pointer-events', 'none')
    .merge(dLabels)
    .text(d => d.dir)
    .attr('x', d => d.x)
    .attr('y', d => d.y);
  dLabels.exit().remove();
}

simulation.on('tick', () => {
  link.attr('x1', d => d.source.x).attr('y1', d => d.source.y)
      .attr('x2', d => d.target.x).attr('y2', d => d.target.y);
  node.attr('cx', d => d.x).attr('cy', d => d.y);
  label.attr('x', d => d.x).attr('y', d => d.y);
  updateHulls();
});

// Tooltip on hover
const tooltip = document.getElementById('tooltip');
node.on('mouseover', (e, d) => {
  let html = '<div class="path">' + d.id + '</div>';
  html += '<div class="dir">' + d.dir + ' &mdash; ' + (d.symbols ? d.symbols.length : 0) + ' symbols, ' + d.edgeCount + ' connections</div>';
  if (d.symbols && d.symbols.length > 0) {
    html += '<div class="sym-list">';
    const show = d.symbols.slice(0, 8);
    show.forEach(s => { html += '<div class="sym">' + s.name + ' <span class="sym-kind">' + s.kind + ':' + s.line + '</span></div>'; });
    if (d.symbols.length > 8) html += '<div class="sym-kind">... +' + (d.symbols.length - 8) + ' more</div>';
    html += '</div>';
  }
  tooltip.innerHTML = html;
  tooltip.style.display = 'block';
}).on('mousemove', (e) => {
  tooltip.style.left = Math.min(e.clientX + 12, width - 370) + 'px';
  tooltip.style.top = (e.clientY + 12) + 'px';
}).on('mouseout', () => { tooltip.style.display = 'none'; });

// Click: highlight connections + open sidebar
node.on('click', (e, d) => {
  e.stopPropagation();
  const imports = [];
  const importedBy = [];
  const connectedIds = new Set([d.id]);

  data.edges.forEach(edge => {
    const src = typeof edge.source === 'object' ? edge.source.id : edge.source;
    const tgt = typeof edge.target === 'object' ? edge.target.id : edge.target;
    if (src === d.id) { connectedIds.add(tgt); imports.push(tgt); }
    if (tgt === d.id) { connectedIds.add(src); importedBy.push(src); }
  });

  // Highlight
  node.attr('opacity', n => connectedIds.has(n.id) ? 1 : 0.08)
      .attr('stroke', n => n.id === d.id ? '#58a6ff' : '#0d1117')
      .attr('stroke-width', n => n.id === d.id ? 3 : 2);
  label.attr('opacity', n => connectedIds.has(n.id) ? 1 : 0.04);
  link.attr('stroke-opacity', l => {
    const src = typeof l.source === 'object' ? l.source.id : l.source;
    const tgt = typeof l.target === 'object' ? l.target.id : l.target;
    return (src === d.id || tgt === d.id) ? 0.7 : 0.03;
  }).attr('stroke', l => {
    const src = typeof l.source === 'object' ? l.source.id : l.source;
    const tgt = typeof l.target === 'object' ? l.target.id : l.target;
    if (src === d.id) return '#58a6ff';
    if (tgt === d.id) return '#f97583';
    return '#21262d';
  }).attr('stroke-width', l => {
    const src = typeof l.source === 'object' ? l.source.id : l.source;
    const tgt = typeof l.target === 'object' ? l.target.id : l.target;
    return (src === d.id || tgt === d.id) ? 1.5 : 0.5;
  });

  // Sidebar
  document.getElementById('sb-file').textContent = d.label;
  document.getElementById('sb-path').textContent = d.id;

  let symHTML = '';
  if (d.symbols) {
    d.symbols.forEach(s => {
      symHTML += '<div class="sym-item"><span class="sym-name">' + s.name + '</span><span class="sym-meta">' + s.kind + ' :' + s.line + '</span></div>';
    });
  }
  document.getElementById('sb-symbols').innerHTML = symHTML;

  const makeConnList = (list, containerId) => {
    const el = document.getElementById(containerId);
    if (list.length === 0) { el.parentElement.style.display = 'none'; return; }
    el.parentElement.style.display = 'block';
    el.innerHTML = list.map(f => '<div class="conn-item" onclick="focusNode(\'' + f.replace(/'/g, "\\'") + '\')">' + f + '</div>').join('');
  };
  makeConnList(imports, 'sb-imports');
  makeConnList(importedBy, 'sb-importedby');

  document.getElementById('sidebar').style.display = 'block';
});

// Single-click background to reset
svg.on('click', (e) => {
  if (e.target.tagName === 'svg' || e.target.tagName === 'rect') resetView();
});

function resetView() {
  node.attr('opacity', 1).attr('stroke', '#0d1117').attr('stroke-width', 2);
  label.attr('opacity', 1);
  link.attr('stroke-opacity', 0.4).attr('stroke', '#21262d')
      .attr('stroke-width', d => Math.max(0.5, Math.min(d.weight * 0.5, 2)));
  closeSidebar();
}

function closeSidebar() {
  document.getElementById('sidebar').style.display = 'none';
}

// Focus on a node from sidebar click
window.focusNode = function(fileId) {
  const nodeData = data.nodes.find(n => n.id === fileId);
  if (!nodeData) return;
  // Simulate click
  const fakeEvent = { stopPropagation: () => {} };
  node.filter(n => n.id === fileId).dispatch('click');
  // Pan to it
  if (nodeData.x != null) {
    svg.transition().duration(500).call(zoom.transform,
      d3.zoomIdentity.translate(width/2 - nodeData.x, height/2 - nodeData.y).scale(1.5)
    );
  }
};

// Search
const searchInput = document.getElementById('search');
searchInput.addEventListener('input', (e) => {
  const query = e.target.value.toLowerCase();
  if (!query) { resetView(); return; }

  const matchIds = new Set();
  data.nodes.forEach(n => {
    if (n.label.toLowerCase().includes(query) || n.id.toLowerCase().includes(query)) matchIds.add(n.id);
    // Also match symbol names inside files
    if (n.symbols) n.symbols.forEach(s => { if (s.name.toLowerCase().includes(query)) matchIds.add(n.id); });
  });

  const expandedIds = new Set(matchIds);
  data.edges.forEach(edge => {
    const src = typeof edge.source === 'object' ? edge.source.id : edge.source;
    const tgt = typeof edge.target === 'object' ? edge.target.id : edge.target;
    if (matchIds.has(src)) expandedIds.add(tgt);
    if (matchIds.has(tgt)) expandedIds.add(src);
  });

  node.attr('opacity', n => matchIds.has(n.id) ? 1 : expandedIds.has(n.id) ? 0.3 : 0.04)
      .attr('stroke', n => matchIds.has(n.id) ? '#58a6ff' : '#0d1117')
      .attr('stroke-width', n => matchIds.has(n.id) ? 3 : 2);
  label.attr('opacity', n => matchIds.has(n.id) ? 1 : expandedIds.has(n.id) ? 0.2 : 0.02);
  link.attr('stroke-opacity', l => {
    const src = typeof l.source === 'object' ? l.source.id : l.source;
    const tgt = typeof l.target === 'object' ? l.target.id : l.target;
    return (matchIds.has(src) || matchIds.has(tgt)) ? 0.7 : 0.02;
  });

  if (matchIds.size > 0) {
    const firstId = [...matchIds][0];
    const firstNode = data.nodes.find(n => n.id === firstId);
    if (firstNode && firstNode.x != null) {
      svg.transition().duration(500).call(zoom.transform,
        d3.zoomIdentity.translate(width/2 - firstNode.x, height/2 - firstNode.y)
      );
    }
  }
});

document.addEventListener('keydown', (e) => {
  if (e.key === '/' && document.activeElement !== searchInput) { e.preventDefault(); searchInput.focus(); }
  if (e.key === 'Escape') { searchInput.value = ''; resetView(); searchInput.blur(); }
});

document.getElementById('stats').textContent = data.nodes.length + ' files, ' + data.edges.length + ' connections';

// Build legend from directory clusters
const legendEl = document.getElementById('legend');
dirs.forEach(d => {
  const item = document.createElement('div');
  item.className = 'legend-item';
  item.innerHTML = '<div class="legend-dot" style="background:' + dirColors[d] + '"></div>' + d;
  legendEl.appendChild(item);
});
</script>
</body>
</html>`
