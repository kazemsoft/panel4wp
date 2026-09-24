const sidebarPreferenceKey = "panel4wp.sidebar.collapsed";
const root = document.documentElement;

try {
  if (localStorage.getItem(sidebarPreferenceKey) === "true") {
    root.classList.add("sidebar-collapsed");
  }
} catch (_) {
  // The panel remains fully usable when browser storage is unavailable.
}

document.addEventListener("DOMContentLoaded", () => {
  const toggle = document.querySelector("[data-sidebar-toggle]");
  if (!toggle) return;

  const sync = () => {
    const collapsed = root.classList.contains("sidebar-collapsed");
    toggle.setAttribute("aria-expanded", String(!collapsed));
    toggle.setAttribute(
      "aria-label",
      collapsed ? toggle.dataset.expandLabel : toggle.dataset.collapseLabel,
    );
    toggle.title = toggle.getAttribute("aria-label");
  };

  toggle.addEventListener("click", () => {
    root.classList.toggle("sidebar-collapsed");
    try {
      localStorage.setItem(
        sidebarPreferenceKey,
        String(root.classList.contains("sidebar-collapsed")),
      );
    } catch (_) {
      // Ignore storage failures; the current-page interaction still works.
    }
    sync();
  });

  sync();
});
