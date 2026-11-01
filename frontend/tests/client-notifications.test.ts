import assert from "node:assert/strict";
import { test } from "node:test";
import { showToast } from "../src/lib/client-notifications";

function installBrowser(inlineMessage?: string) {
  const events: Event[] = [];
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
  const originalDocument = Object.getOwnPropertyDescriptor(globalThis, "document");

  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: {
      requestAnimationFrame: (callback: FrameRequestCallback) => {
        callback(0);
        return 1;
      },
      dispatchEvent: (event: Event) => {
        events.push(event);
        return true;
      },
    },
  });
  Object.defineProperty(globalThis, "document", {
    configurable: true,
    value: {
      querySelectorAll: () => inlineMessage === undefined ? [] : [{
        getClientRects: () => [{}],
        textContent: inlineMessage,
      }],
    },
  });

  return {
    events,
    restore() {
      if (originalWindow) Object.defineProperty(globalThis, "window", originalWindow);
      else Reflect.deleteProperty(globalThis, "window");
      if (originalDocument) Object.defineProperty(globalThis, "document", originalDocument);
      else Reflect.deleteProperty(globalThis, "document");
    },
  };
}

test("does not show an error toast when the same error is already inline", () => {
  const browser = installBrowser("SMTP key is missing");
  try {
    showToast("Email settings failed", "SMTP key is missing", "error");
    assert.equal(browser.events.length, 0);
  } finally {
    browser.restore();
  }
});

test("shows an error toast when no matching inline error exists", () => {
  const browser = installBrowser("A different error");
  try {
    showToast("Email settings failed", "SMTP key is missing", "error");
    assert.equal(browser.events.length, 1);
    assert.equal((browser.events[0] as CustomEvent).detail.body, "SMTP key is missing");
  } finally {
    browser.restore();
  }
});
