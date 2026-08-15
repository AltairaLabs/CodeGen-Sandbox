import mermaid from 'https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs';

function cssVar(name, fallback) {
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return v || fallback;
}

function getThemeConfig() {
  // Read straight from the Atlas tokens so the diagrams re-skin with the
  // rest of the page when data-theme flips. Fallbacks are the Atlas
  // light-sky values, used only if the token sheet has not parsed yet.
  const line = cssVar('--starlight-500', '#3b82f6');
  const textColor = cssVar('--text-body', '#1e293b');
  const labelColor = cssVar('--text-faint', '#5E708C');

  return {
    startOnLoad: false,
    // 'handDrawn' fights the instrument-grade Atlas register.
    look: 'classic',
    theme: 'base',
    themeVariables: {
      // No fills - transparent backgrounds over the Atlas canvas.
      primaryColor: 'transparent',
      secondaryColor: 'transparent',
      tertiaryColor: 'transparent',
      // Starlight-blue borders, matching ConstellationGraph edges.
      primaryBorderColor: line,
      secondaryBorderColor: line,
      tertiaryBorderColor: line,
      // Text colors - match current mode
      primaryTextColor: textColor,
      secondaryTextColor: textColor,
      tertiaryTextColor: textColor,
      // Lines and edges
      lineColor: line,
      // Edge labels - no background
      edgeLabelBackground: 'transparent',
      labelBackground: 'transparent',
      labelTextColor: labelColor,
      // Background
      background: 'transparent',
      mainBkg: 'transparent',
      nodeBkg: 'transparent',
      // Git graph specific
      git0: line,
      git1: line,
      gitBranchLabel0: line,
      gitBranchLabel1: line,
      commitLabelBackground: 'transparent',
      // Fonts
      fontFamily: cssVar('--font-sans', 'system-ui, -apple-system, sans-serif'),
    },
  };
}

mermaid.initialize(getThemeConfig());

async function renderMermaid() {
  // Starlight uses expressive-code which wraps code in a complex structure
  // Find all pre elements with data-language="mermaid" that haven't been processed yet
  const mermaidBlocks = document.querySelectorAll('pre[data-language="mermaid"]:not([data-mermaid-processed])');

  for (const pre of mermaidBlocks) {
    // Mark as processed immediately to prevent duplicate processing
    pre.setAttribute('data-mermaid-processed', 'true');
    // Extract text content from all the span elements inside
    // The structure is: pre > code > div.ec-line > div.code > span
    const lines = pre.querySelectorAll('.ec-line');
    let content = '';

    if (lines.length > 0) {
      // Expressive-code structure
      lines.forEach(line => {
        const codeDiv = line.querySelector('.code');
        if (codeDiv) {
          content += codeDiv.textContent + '\n';
        }
      });
    } else {
      // Fallback to simple structure
      const code = pre.querySelector('code');
      content = code ? code.textContent : pre.textContent;
    }

    content = content.trim();
    if (!content) continue;

    // Create mermaid container
    const div = document.createElement('div');
    div.className = 'mermaid';
    div.textContent = content;

    // Find the outermost wrapper
    const figure = pre.closest('figure.frame');
    const wrapper = figure ? figure.closest('.expressive-code') : null;
    const targetElement = wrapper || figure || pre;

    // Create a wrapper div to maintain proper block-level spacing
    const containerDiv = document.createElement('div');
    containerDiv.className = 'mermaid-container';
    containerDiv.style.cssText = 'display: block; margin: 1rem 0;';
    containerDiv.appendChild(div);

    // Hide the original element instead of removing it to preserve DOM/CSS structure
    // This prevents breaking CSS selectors that depend on sibling relationships
    targetElement.style.display = 'none';
    targetElement.setAttribute('data-mermaid-hidden', 'true');

    // Insert the mermaid diagram after the hidden element
    targetElement.parentNode.insertBefore(containerDiv, targetElement.nextSibling);
  }

  // Run mermaid if we found any diagrams
  const diagrams = document.querySelectorAll('.mermaid');
  if (diagrams.length > 0) {
    try {
      await mermaid.run();
    } catch (e) {
      console.error('Mermaid rendering error:', e);
    }
  }
}

// Run on initial load
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', renderMermaid);
} else {
  renderMermaid();
}

// Re-run on Astro view transitions
document.addEventListener('astro:page-load', renderMermaid);

// Undo a previous render so the diagrams can be drawn again with new theme
// colours. renderMermaid() deliberately skips anything already marked
// processed, so without this the theme observer re-initialises mermaid but
// leaves the old SVG on the page — which showed up as dark-on-dark labels
// after switching to the night sky.
function resetMermaid() {
  document.querySelectorAll('.mermaid-container').forEach((c) => c.remove());
  document.querySelectorAll('pre[data-mermaid-processed]').forEach((pre) => {
    pre.removeAttribute('data-mermaid-processed');
  });
  document.querySelectorAll('[data-mermaid-hidden]').forEach((el) => {
    el.style.display = '';
    el.removeAttribute('data-mermaid-hidden');
  });
}

// Re-render when theme changes
const observer = new MutationObserver((mutations) => {
  for (const mutation of mutations) {
    if (mutation.attributeName === 'data-theme') {
      mermaid.initialize(getThemeConfig());
      resetMermaid();
      renderMermaid();
    }
  }
});
observer.observe(document.documentElement, { attributes: true });
