import type { TFunction } from "i18next";

import type { SignatureOp } from "@multica/core/types";

/**
 * Explicit-switch helper for dynamic op labels (0.5.132 discipline):
 * template-literal selectors are rejected by the i18next type narrowing,
 * so every dynamic key goes through a switch whose arms are literal
 * selectors. Adding an op means adding an arm here — the exhaustive
 * return keeps misses loud at the type level.
 */
export function signatureOpLabel(t: TFunction<"signature">, op: SignatureOp): string {
  switch (op) {
    case "offensive_drill":
      return t(($) => $.op_offensive_drill);
    case "create_agent":
      return t(($) => $.op_create_agent);
    case "create_squad":
      return t(($) => $.op_create_squad);
    case "create_skill":
      return t(($) => $.op_create_skill);
    case "install_plugin":
      return t(($) => $.op_install_plugin);
    case "lab_delegate":
      return t(($) => $.op_lab_delegate);
  }
}
