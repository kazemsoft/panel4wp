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
  document.querySelectorAll("form[data-disable-on-submit]").forEach((form) => {
    form.addEventListener("submit", () => {
      const button = form.querySelector('button[type="submit"], button:not([type])');
      if (!button || button.disabled) return;

      button.disabled = true;
      button.setAttribute("aria-busy", "true");
      button.classList.add("is-submitting");
      const label = button.querySelector(".button-label");
      if (label && form.dataset.progressLabel) {
        label.textContent = form.dataset.progressLabel;
      }
    });
  });

  document.querySelectorAll("[data-modal-open]").forEach((button) => {
    button.addEventListener("click", () => {
      const dialog = document.getElementById(button.dataset.modalOpen);
      if (dialog instanceof HTMLDialogElement) dialog.showModal();
    });
  });

  document.querySelectorAll("[data-copy-text]").forEach((button) => {
    button.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(button.dataset.copyText);
      } catch (_) {
        // The domain remains selectable when clipboard access is unavailable.
      }
    });
  });

  document.querySelectorAll("dialog.confirm-dialog").forEach((dialog) => {
    dialog.querySelectorAll("[data-modal-close]").forEach((button) => {
      button.addEventListener("click", () => dialog.close());
    });
    dialog.addEventListener("click", (event) => {
      if (event.target === dialog) dialog.close();
    });
    dialog.addEventListener("close", () => {
      dialog.querySelector("form[data-confirm-domain]")?.reset();
    });
  });

  document.querySelectorAll("form[data-confirm-domain]").forEach((form) => {
    const input = form.querySelector("[data-confirm-input]");
    const submit = form.querySelector("[data-confirm-submit]");
    if (!input || !submit) return;

    const syncConfirmation = () => {
      submit.disabled = input.value !== form.dataset.confirmDomain;
    };
    input.addEventListener("input", syncConfirmation);
    form.addEventListener("reset", () => window.setTimeout(syncConfirmation));
    syncConfirmation();
  });

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
