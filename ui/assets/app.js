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

  const fileRows = [...document.querySelectorAll("[data-file-row]")];
  const fileContextMenu = document.querySelector("[data-file-context-menu]");
  const fileDownloadForm = document.querySelector("[data-file-download-form]");
  let selectedFileRow = null;

  const closeFileContextMenu = () => {
    if (fileContextMenu) fileContextMenu.hidden = true;
  };

  const selectFileRow = (row) => {
    if (!row) return;
    fileRows.forEach((candidate) => {
      const selected = candidate === row;
      candidate.classList.toggle("selected", selected);
      candidate.setAttribute("aria-selected", String(selected));
    });
    selectedFileRow = row;
    document.querySelectorAll("[data-selected-path]").forEach((input) => {
      input.value = row.dataset.filePath;
    });
    document.querySelectorAll("[data-selected-name]").forEach((element) => {
      element.textContent = row.dataset.fileName;
    });
    const deleteQuestion = document.querySelector("[data-delete-question]");
    if (deleteQuestion) {
      deleteQuestion.textContent = deleteQuestion.dataset.deleteQuestionTemplate.replace(
        "%s",
        row.dataset.fileName,
      );
    }
    document.querySelectorAll("[data-selection-action]").forEach((button) => {
      const fileOnly = button.hasAttribute("data-requires-file");
      const mutableOnly = button.hasAttribute("data-requires-mutable");
      button.disabled =
        (fileOnly && row.dataset.fileType !== "file") ||
        (mutableOnly && row.dataset.fileType === "link");
    });
  };

  const openFileContextMenu = (row, x, y) => {
    if (!fileContextMenu) return;
    selectFileRow(row);
    const canOpen = Boolean(row.dataset.fileOpen);
    const isFile = row.dataset.fileType === "file";
    const isMutable = row.dataset.fileType !== "link";
    fileContextMenu.querySelector("[data-context-open]").hidden = !canOpen;
    fileContextMenu.querySelector("[data-context-download]").hidden = !isFile;
    fileContextMenu.querySelectorAll("[data-context-mutable]").forEach((item) => {
      item.hidden = !isMutable;
    });
    fileContextMenu.hidden = false;
    const bounds = fileContextMenu.getBoundingClientRect();
    fileContextMenu.style.left = `${Math.min(x, window.innerWidth - bounds.width - 8)}px`;
    fileContextMenu.style.top = `${Math.min(y, window.innerHeight - bounds.height - 8)}px`;
  };

  fileRows.forEach((row) => {
    row.addEventListener("click", () => selectFileRow(row));
    row.addEventListener("dblclick", () => {
      if (row.dataset.fileOpen) window.location.assign(row.dataset.fileOpen);
    });
    row.addEventListener("contextmenu", (event) => {
      event.preventDefault();
      openFileContextMenu(row, event.clientX, event.clientY);
    });
    row.addEventListener("keydown", (event) => {
      if (event.key === " " || event.key === "Spacebar") {
        event.preventDefault();
        selectFileRow(row);
      } else if (event.key === "Enter" && row.dataset.fileOpen) {
        window.location.assign(row.dataset.fileOpen);
      } else if (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10")) {
        event.preventDefault();
        const bounds = row.getBoundingClientRect();
        openFileContextMenu(row, bounds.left + 24, bounds.top + 24);
      }
    });
  });

  fileContextMenu?.querySelector("[data-context-open]")?.addEventListener("click", () => {
    if (selectedFileRow?.dataset.fileOpen) window.location.assign(selectedFileRow.dataset.fileOpen);
  });
  fileContextMenu?.querySelector("[data-context-download]")?.addEventListener("click", () => {
    fileDownloadForm?.requestSubmit();
  });
  document.addEventListener("click", (event) => {
    if (!fileContextMenu?.contains(event.target)) closeFileContextMenu();
  });
  window.addEventListener("blur", closeFileContextMenu);
  window.addEventListener("resize", closeFileContextMenu);
  window.addEventListener("scroll", closeFileContextMenu, true);

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
