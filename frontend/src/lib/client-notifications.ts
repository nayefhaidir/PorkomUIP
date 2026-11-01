export type ToastKind = "success" | "error" | "info";

export function showToast(title: string, body: string, kind: ToastKind = "success") {
  if (typeof window === "undefined") return;
  const dispatch = () => window.dispatchEvent(new CustomEvent("app:toast", { detail: { title, body, kind } }));
  if (kind !== "error") {
    dispatch();
    return;
  }

  window.requestAnimationFrame(() => {
    const alreadyInline = Array.from(document.querySelectorAll<HTMLElement>(".form-error, [role='alert']"))
      .some(element => element.getClientRects().length > 0 && element.textContent?.trim() === body.trim());
    if (!alreadyInline) dispatch();
  });
}
