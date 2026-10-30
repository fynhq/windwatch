(function () {
  "use strict";

  function refreshToggles(selectDefaultProxy) {
    var toggles = document.querySelectorAll("[data-toggle-target]");
    for (var i = 0; i < toggles.length; i++) {
      var toggle = toggles[i];
      var target = document.getElementById(toggle.getAttribute("data-toggle-target"));
      if (!target) {
        continue;
      }
      if (selectDefaultProxy && toggle.checked && target.tagName === "SELECT" &&
          target.value === "" && target.options.length > 1) {
        target.selectedIndex = 1;
      }
      target.disabled = !toggle.checked;
    }
  }

  function randomHex(byteCount) {
    var bytes = new Uint8Array(byteCount);
    if (window.crypto && window.crypto.getRandomValues) {
      window.crypto.getRandomValues(bytes);
    } else {
      for (var i = 0; i < bytes.length; i++) {
        bytes[i] = Math.floor(Math.random() * 256);
      }
    }
    var value = "";
    for (var j = 0; j < bytes.length; j++) {
      value += ("0" + bytes[j].toString(16)).slice(-2);
    }
    return value;
  }

  function toastRegion() {
    return document.querySelector("[data-toast-region]");
  }

  function dismissToast(toast) {
    if (!toast || toast.classList.contains("is-leaving")) {
      return;
    }
    toast.classList.add("is-leaving");
    window.setTimeout(function () {
      if (toast.parentNode) {
        toast.parentNode.removeChild(toast);
      }
    }, 160);
  }

  function scheduleToast(toast, timeout) {
    if (!isFinite(timeout) || timeout <= 0) {
      return;
    }
    window.setTimeout(function () {
      dismissToast(toast);
    }, timeout);
  }

  function showToast(message, kind, timeout) {
    var region = toastRegion();
    if (!region || !message) {
      return;
    }
    var error = kind === "error";
    var toast = document.createElement("div");
    toast.className = "toast " + (error ? "toast-error" : "toast-success");
    toast.setAttribute("role", error ? "alert" : "status");
    toast.setAttribute("data-toast", "");
    var text = document.createElement("span");
    text.className = "toast-message";
    text.textContent = message;
    var close = document.createElement("button");
    close.className = "toast-close";
    close.type = "button";
    close.setAttribute("data-toast-close", "");
    close.setAttribute("aria-label", region.getAttribute("data-close-label") || "Dismiss");
    close.textContent = "\u00d7";
    toast.appendChild(text);
    toast.appendChild(close);
    region.appendChild(toast);
    scheduleToast(toast, typeof timeout === "number" ? timeout : (error ? 0 : 5000));
  }

  function initToasts() {
    var region = toastRegion();
    if (!region) {
      return;
    }
    var toasts = region.querySelectorAll("[data-toast]");
    for (var i = 0; i < toasts.length; i++) {
      scheduleToast(toasts[i], parseInt(toasts[i].getAttribute("data-timeout"), 10));
    }
    try {
      var currentURL = new URL(window.location.href);
      if (currentURL.searchParams.has("notice")) {
        currentURL.searchParams.delete("notice");
        window.history.replaceState(null, "", currentURL.pathname + currentURL.search + currentURL.hash);
      }
    } catch (_) {
      // Keeping the URL is harmless when history is unavailable.
    }
    region.addEventListener("click", function (event) {
      var button = event.target.closest("[data-toast-close]");
      if (button) {
        dismissToast(button.closest("[data-toast]"));
      }
    });
  }

  function initDialogs() {
    var openers = document.querySelectorAll("[data-dialog-open]");
    for (var i = 0; i < openers.length; i++) {
      openers[i].addEventListener("click", function () {
        var dialog = document.getElementById(this.getAttribute("data-dialog-open"));
        if (dialog && typeof dialog.showModal === "function") {
          dialog.showModal();
        }
      });
    }
    var closers = document.querySelectorAll("[data-dialog-close]");
    for (var j = 0; j < closers.length; j++) {
      closers[j].addEventListener("click", function () {
        var dialog = this.closest("dialog");
        if (dialog) {
          dialog.close();
        }
      });
    }
    var dialogs = document.querySelectorAll("dialog");
    for (var k = 0; k < dialogs.length; k++) {
      dialogs[k].addEventListener("click", function (event) {
        if (event.target === this) {
          this.close();
        }
      });
    }
    var autoOpen = document.querySelector("dialog[data-auto-open]");
    if (autoOpen && typeof autoOpen.showModal === "function") {
      autoOpen.removeAttribute("data-auto-open");
      autoOpen.showModal();
    }
  }

  function fallbackCopy(value) {
    var textarea = document.createElement("textarea");
    textarea.value = value;
    textarea.setAttribute("readonly", "");
    textarea.style.position = "fixed";
    textarea.style.top = "-1000px";
    document.body.appendChild(textarea);
    textarea.select();
    var copied = false;
    try {
      copied = document.execCommand("copy");
    } catch (_) {
      copied = false;
    }
    document.body.removeChild(textarea);
    return copied;
  }

  function copyText(value) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(value).then(function () {
        return true;
      }, function () {
        return fallbackCopy(value);
      });
    }
    return Promise.resolve(fallbackCopy(value));
  }

  function initCopyFields() {
    var region = toastRegion();
    var successLabel = region ? region.getAttribute("data-copy-success") : "";
    var failedLabel = region ? region.getAttribute("data-copy-failed") : "";
    var triggers = document.querySelectorAll("[data-copy-value]");
    for (var i = 0; i < triggers.length; i++) {
      triggers[i].addEventListener("click", function () {
        var value = this.getAttribute("data-copy-value");
        if (!value) {
          return;
        }
        copyText(value).then(function (copied) {
          showToast(copied ? successLabel : failedLabel, copied ? "success" : "error", copied ? 3500 : 5000);
        });
      });
    }
  }

  function initEventDialog() {
    var dialog = document.querySelector("[data-event-dialog]");
    if (!dialog || typeof dialog.showModal !== "function") {
      return;
    }
    var fields = {};
    var nodes = dialog.querySelectorAll("[data-event-field]");
    for (var i = 0; i < nodes.length; i++) {
      fields[nodes[i].getAttribute("data-event-field")] = nodes[i];
    }
    var triggers = document.querySelectorAll("[data-event-detail]");
    for (var j = 0; j < triggers.length; j++) {
      triggers[j].addEventListener("click", function () {
        var values = {
          monitor: this.getAttribute("data-event-monitor"),
          type: this.getAttribute("data-event-type"),
          started: this.getAttribute("data-event-started"),
          ended: this.getAttribute("data-event-ended"),
          duration: this.getAttribute("data-event-duration"),
          message: this.getAttribute("data-event-message")
        };
        for (var key in values) {
          if (Object.prototype.hasOwnProperty.call(fields, key)) {
            fields[key].textContent = values[key] || "\u2014";
          }
        }
        dialog.showModal();
      });
    }
  }

  function initNotificationContentDialog() {
    var dialog = document.querySelector("[data-notification-content-dialog]");
    if (!dialog || typeof dialog.showModal !== "function") {
      return;
    }
    var fields = {};
    var nodes = dialog.querySelectorAll("[data-notification-content-field]");
    for (var i = 0; i < nodes.length; i++) {
      fields[nodes[i].getAttribute("data-notification-content-field")] = nodes[i];
    }
    var triggers = document.querySelectorAll("[data-notification-content]");
    for (var j = 0; j < triggers.length; j++) {
      triggers[j].addEventListener("click", function () {
        var meta = [this.getAttribute("data-time"), this.getAttribute("data-status")]
          .filter(function (value) { return value; })
          .join(" \u00b7 ");
        var values = {
          channel: this.getAttribute("data-channel"),
          meta: meta,
          payload: this.getAttribute("data-payload")
        };
        for (var key in values) {
          if (Object.prototype.hasOwnProperty.call(fields, key)) {
            fields[key].textContent = values[key] || "\u2014";
          }
        }
        dialog.showModal();
      });
    }
  }

  function initHistoryTabs() {
    var tablist = document.querySelector("[data-history-tabs]");
    if (!tablist) {
      return;
    }
    var card = tablist.closest("[data-history-card]");
    if (!card) {
      return;
    }
    var tabs = Array.prototype.slice.call(tablist.querySelectorAll("[data-history-tab]"));
    var panels = card.querySelectorAll("[data-history-panel]");
    if (tabs.length === 0) {
      return;
    }

    function activate(id, focus) {
      for (var i = 0; i < tabs.length; i++) {
        var tab = tabs[i];
        var selected = tab.getAttribute("data-history-tab") === id;
        tab.classList.toggle("is-active", selected);
        tab.setAttribute("aria-selected", selected ? "true" : "false");
        tab.tabIndex = selected ? 0 : -1;
        if (selected && focus) {
          tab.focus();
        }
      }
      for (var j = 0; j < panels.length; j++) {
        panels[j].hidden = panels[j].id !== id;
      }
    }

    for (var i = 0; i < tabs.length; i++) {
      tabs[i].addEventListener("click", function () {
        activate(this.getAttribute("data-history-tab"), false);
      });
      tabs[i].addEventListener("keydown", function (event) {
        var index = tabs.indexOf(this);
        var next = index;
        if (event.key === "ArrowRight") {
          next = (index + 1) % tabs.length;
        } else if (event.key === "ArrowLeft") {
          next = (index - 1 + tabs.length) % tabs.length;
        } else if (event.key === "Home") {
          next = 0;
        } else if (event.key === "End") {
          next = tabs.length - 1;
        } else {
          return;
        }
        event.preventDefault();
        activate(tabs[next].getAttribute("data-history-tab"), true);
      });
    }

    activate(tabs[0].getAttribute("data-history-tab"), false);
  }

  function storageGet(key) {
    try {
      return window.sessionStorage.getItem(key) || "";
    } catch (_) {
      return "";
    }
  }

  function storageSet(key, value) {
    try {
      window.sessionStorage.setItem(key, value);
    } catch (_) {
      // Filter state is optional; storage can be unavailable in private modes.
    }
  }

  function formatShowing(template, visible, total) {
    if (!template) {
      return visible + " / " + total;
    }
    return template.replace("%d", visible).replace("%d", total);
  }

  function setFilterButtons(buttons, active) {
    for (var i = 0; i < buttons.length; i++) {
      var button = buttons[i];
      var selected = button.getAttribute("data-monitor-filter") === active ||
        button.getAttribute("data-event-filter") === active;
      button.classList.toggle("is-active", selected);
      button.setAttribute("aria-pressed", selected ? "true" : "false");
      button.disabled = false;
    }
  }

  function initMonitorFilters() {
    var toolbar = document.querySelector("[data-dashboard-toolbar]");
    if (!toolbar) {
      return;
    }
    var cards = Array.prototype.slice.call(document.querySelectorAll("[data-monitor-card]"));
    var buttons = Array.prototype.slice.call(document.querySelectorAll("[data-monitor-filter]"));
    var search = toolbar.querySelector("[data-monitor-search]");
    var result = toolbar.querySelector("[data-monitor-result]");
    var empty = document.querySelector("[data-monitor-empty]");
    var clear = empty ? empty.querySelector("[data-clear-filters]") : null;
    if (cards.length === 0) {
      toolbar.hidden = true;
      return;
    }
    toolbar.hidden = false;
    if (buttons.length === 0) {
      return;
    }
    var state = storageGet("windwatch.dashboard.filter") || "all";
    var query = storageGet("windwatch.dashboard.q") || "";

    function apply() {
      var visible = 0;
      for (var i = 0; i < cards.length; i++) {
        var card = cards[i];
        var matchStatus = state === "all" || card.getAttribute("data-status") === state;
        var haystack = (card.getAttribute("data-search") || "").toLowerCase();
        var matchQuery = !query || haystack.indexOf(query) !== -1;
        var match = matchStatus && matchQuery;
        card.hidden = !match;
        if (match) {
          visible++;
        }
      }
      setFilterButtons(buttons, state);
      if (result) {
        result.textContent = formatShowing(result.getAttribute("data-result-template"), visible, cards.length);
      }
      if (empty) {
        empty.hidden = visible !== 0;
      }
    }

    for (var i = 0; i < buttons.length; i++) {
      buttons[i].addEventListener("click", function () {
        state = this.getAttribute("data-monitor-filter");
        storageSet("windwatch.dashboard.filter", state);
        apply();
      });
    }
    if (search) {
      search.disabled = false;
      search.value = query;
      search.addEventListener("input", function () {
        query = this.value.trim().toLowerCase();
        storageSet("windwatch.dashboard.q", query);
        apply();
      });
    }
    if (clear) {
      clear.addEventListener("click", function () {
        state = "all";
        query = "";
        storageSet("windwatch.dashboard.filter", state);
        storageSet("windwatch.dashboard.q", query);
        if (search) {
          search.value = "";
        }
        apply();
        if (search) {
          search.focus();
        }
      });
    }
    apply();
  }

  function initEventFilters() {
    var toolbar = document.querySelector("[data-events-toolbar]");
    if (!toolbar) {
      return;
    }
    var rows = Array.prototype.slice.call(document.querySelectorAll("[data-event-row]"));
    var buttons = Array.prototype.slice.call(toolbar.querySelectorAll("[data-event-filter]"));
    var search = toolbar.querySelector("[data-event-search]");
    var result = toolbar.querySelector("[data-events-result]");
    var empty = document.querySelector("[data-events-empty]");
    var clear = empty ? empty.querySelector("[data-clear-filters]") : null;
    var state = storageGet("windwatch.events.filter") || "all";
    var query = storageGet("windwatch.events.q") || "";
    if (rows.length === 0 || buttons.length === 0) {
      return;
    }

    function apply() {
      var visible = 0;
      for (var i = 0; i < rows.length; i++) {
        var row = rows[i];
        var type = row.getAttribute("data-event-type") || "";
        var ongoing = row.getAttribute("data-event-ongoing") === "true";
        var matchType = state === "all" ||
          (state === "down" && type === "status-down") ||
          (state === "up" && type === "status-up") ||
          (state === "ongoing" && ongoing);
        var haystack = (row.getAttribute("data-search") || "").toLowerCase();
        var matchQuery = !query || haystack.indexOf(query) !== -1;
        var match = matchType && matchQuery;
        row.hidden = !match;
        if (match) {
          visible++;
        }
      }
      setFilterButtons(buttons, state);
      if (result) {
        result.textContent = formatShowing(result.getAttribute("data-result-template"), visible, rows.length);
      }
      if (empty) {
        empty.hidden = visible !== 0;
      }
    }

    for (var i = 0; i < buttons.length; i++) {
      buttons[i].addEventListener("click", function () {
        state = this.getAttribute("data-event-filter");
        storageSet("windwatch.events.filter", state);
        apply();
      });
    }
    if (search) {
      search.disabled = false;
      search.value = query;
      search.addEventListener("input", function () {
        query = this.value.trim().toLowerCase();
        storageSet("windwatch.events.q", query);
        apply();
      });
    }
    if (clear) {
      clear.addEventListener("click", function () {
        state = "all";
        query = "";
        storageSet("windwatch.events.filter", state);
        storageSet("windwatch.events.q", query);
        if (search) {
          search.value = "";
        }
        apply();
        if (search) {
          search.focus();
        }
      });
    }
    apply();
  }

  function initAutoRefresh() {
    var seconds = parseInt(document.body.getAttribute("data-auto-refresh") || "", 10);
    if (!(seconds > 0)) {
      return;
    }
    var meta = document.querySelector('meta[http-equiv="refresh"]');
    if (meta && meta.parentNode) {
      meta.parentNode.removeChild(meta);
    }
    var target = document.body.getAttribute("data-refresh-target") || "";
    var label = null;
    var template = "";
    var justNow = "";
    var startedAt = Date.now();
    var lastAttempt = startedAt;
    var refreshing = false;
    var reloaded = false;

    function refreshLabelRefs() {
      label = document.querySelector("[data-refresh-label]");
      template = label ? label.getAttribute("data-refresh-template") || "" : "";
      justNow = label ? label.getAttribute("data-just-now") || "" : "";
    }

    function updateLabel() {
      if (!label || !template) {
        return;
      }
      var elapsed = Math.max(0, Math.floor((Date.now() - startedAt) / 1000));
      label.textContent = elapsed < 5 && justNow ? justNow : template.replace("%d", elapsed);
    }

    function liveRegion() {
      if (!target) {
        return null;
      }
      return document.querySelector('[data-live-region="' + target + '"]');
    }

    var usePartial = !!(target && liveRegion() && window.fetch && window.DOMParser);
    if (target === "dashboard" && !document.querySelector("[data-dashboard-toolbar]")) {
      usePartial = false;
    }

    function reloadPage() {
      if (reloaded) {
        return;
      }
      reloaded = true;
      window.location.reload();
    }

    function captureFocus() {
      var active = document.activeElement;
      var region = liveRegion();
      if (!active || !region || !region.contains(active)) {
        return null;
      }
      if (active.matches && active.matches("[data-monitor-search]")) {
        return { type: "search", start: active.selectionStart, end: active.selectionEnd };
      }
      var filter = active.closest ? active.closest("[data-monitor-filter]") : null;
      if (filter) {
        return { type: "filter", value: filter.getAttribute("data-monitor-filter") };
      }
      return null;
    }

    function restoreFocus(state) {
      if (!state) {
        return;
      }
      if (state.type === "search") {
        var input = document.querySelector("[data-monitor-search]");
        if (input) {
          input.focus();
          if (input.setSelectionRange && state.start !== null && state.start !== undefined) {
            input.setSelectionRange(state.start, state.end);
          }
        }
        return;
      }
      if (state.type === "filter") {
        var selector = '[data-monitor-filter="' + state.value + '"]';
        var button = document.querySelector(selector);
        if (button) {
          button.focus();
        }
      }
    }

    function replaceFragment(html) {
      var active = document.activeElement;
      if (active && active.matches && active.matches("[data-monitor-search]")) {
        return;
      }
      var parsed = new DOMParser().parseFromString(html, "text/html");
      var next = parsed.querySelector('[data-live-region="' + target + '"]');
      var current = liveRegion();
      if (!next || !current || parsed.querySelector(".app-shell, .auth-header")) {
        reloadPage();
        return;
      }
      var focus = captureFocus();
      current.innerHTML = next.innerHTML;
      refreshLabelRefs();
      if (target === "dashboard") {
        initMonitorFilters();
      }
      restoreFocus(focus);
      startedAt = Date.now();
      updateLabel();
    }

    function fetchFragment() {
      if (refreshing || reloaded) {
        return;
      }
      refreshing = true;
      lastAttempt = Date.now();
      var options = {
        cache: "no-store",
        credentials: "same-origin",
        headers: {}
      };
      options.headers["X-WindWatch-Partial"] = target;
      var controller = typeof AbortController === "function" ? new AbortController() : null;
      if (controller) {
        options.signal = controller.signal;
        window.setTimeout(function () {
          controller.abort();
        }, 10000);
      }
      window.fetch(window.location.href, options).then(function (response) {
        if (response.status === 401 || response.status === 404) {
          reloadPage();
          return null;
        }
        if (!response.ok) {
          return null;
        }
        return response.text();
      }).then(function (html) {
        if (html === null) {
          return;
        }
        replaceFragment(html);
      }).catch(function () {
        // Keep the last good fragment and retry on the next interval.
      }).then(function () {
        refreshing = false;
      });
    }

    function tick() {
      updateLabel();
      if (document.visibilityState === "hidden" || reloaded) {
        return;
      }
      if (usePartial) {
        var active = document.activeElement;
        if (active && active.matches && active.matches("[data-monitor-search]")) {
          return;
        }
        if (!refreshing && Date.now() - lastAttempt >= seconds * 1000) {
          fetchFragment();
        }
        return;
      }
      if (Date.now() - startedAt >= seconds * 1000) {
        reloadPage();
      }
    }

    refreshLabelRefs();
    updateLabel();
    window.setInterval(tick, 1000);
    document.addEventListener("visibilitychange", tick);
  }

  function initSecretGenerators() {
    var buttons = document.querySelectorAll("[data-generate-secret]");
    for (var i = 0; i < buttons.length; i++) {
      buttons[i].addEventListener("click", function () {
        var group = this.closest("[data-secret-group]");
        var input = group ? group.querySelector("input") : null;
        if (input) {
          input.value = randomHex(32);
          input.setAttribute("data-secret-configured", "false");
        }
      });
    }
  }

  function initChannelTypeFields() {
    var selects = document.querySelectorAll("[data-channel-type-select]");
    for (var i = 0; i < selects.length; i++) {
      (function (select) {
        var form = select.closest("form");
        if (!form) {
          return;
        }
        var groups = form.querySelectorAll("[data-channel-fields]");
        var typeField = form.querySelector("[data-channel-type-field]");
        var formatField = form.querySelector("[data-webhook-format-field]");
        function refresh() {
          var active = select.value;
          var webhook = active === "webhook";
          for (var j = 0; j < groups.length; j++) {
            var group = groups[j];
            var match = group.getAttribute("data-channel-fields") === active;
            group.hidden = !match;
            var fields = group.querySelectorAll("input, select, textarea");
            for (var k = 0; k < fields.length; k++) {
              fields[k].disabled = !match;
            }
          }
          if (typeField) {
            typeField.classList.toggle("is-wide", !webhook);
          }
          if (formatField) {
            formatField.hidden = !webhook;
            var formatSelects = formatField.querySelectorAll("select");
            for (var m = 0; m < formatSelects.length; m++) {
              formatSelects[m].disabled = !webhook;
            }
          }
        }
        select.addEventListener("change", refresh);
        refresh();
      })(selects[i]);
    }
  }

  function initWebhookFormatFields() {
    var selects = document.querySelectorAll("[data-webhook-format-select]");
    for (var i = 0; i < selects.length; i++) {
      (function (select) {
        var form = select.closest("form");
        if (!form) {
          return;
        }
        var groups = form.querySelectorAll("[data-webhook-secret]");
        function refresh() {
          var hide = select.value === "wecom";
          for (var j = 0; j < groups.length; j++) {
            groups[j].hidden = hide;
          }
        }
        select.addEventListener("change", refresh);
        refresh();
      })(selects[i]);
    }
  }

  function initConfirmForms() {
    document.addEventListener("submit", function (event) {
      var form = event.target.closest("form[data-confirm]");
      if (!form) {
        return;
      }
      if (!window.confirm(form.getAttribute("data-confirm") || "")) {
        event.preventDefault();
      }
    });
  }

  function initHTTPSChecks() {
    var urlInput = document.querySelector("input[name='url']");
    if (!urlInput) {
      return;
    }
    var cert = document.querySelector("input[name='cert_check']");
    var domain = document.querySelector("input[name='domain_check']");
    if (!cert && !domain) {
      return;
    }
    function refresh() {
      var value = urlInput.value.trim();
      var plainHTTP = /^http:\/\//i.test(value);
      [cert, domain].forEach(function (checkbox) {
        if (!checkbox) {
          return;
        }
        if (plainHTTP) {
          checkbox.checked = false;
        }
        checkbox.disabled = plainHTTP;
      });
      refreshToggles(false);
    }
    urlInput.addEventListener("input", refresh);
    refresh();
  }

  document.addEventListener("DOMContentLoaded", function () {
    refreshToggles(false);
    var toggles = document.querySelectorAll("[data-toggle-target]");
    for (var i = 0; i < toggles.length; i++) {
      toggles[i].addEventListener("change", function () {
        refreshToggles(true);
      });
    }

    initHistoryTabs();
    initMonitorFilters();
    initEventFilters();
    initAutoRefresh();
    initSecretGenerators();
    initChannelTypeFields();
    initWebhookFormatFields();
    initHTTPSChecks();
    initToasts();
    initDialogs();
    initCopyFields();
    initConfirmForms();
    initEventDialog();
    initNotificationContentDialog();
  });
})();
