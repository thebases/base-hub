// Rarely used ModelView dialogs, bundled behind one dynamic import so ModelView can fetch them when idle.
import { DatabaseDuplicateDialog } from "./DatabaseDuplicateDialog.js";
import { DatabaseModuleSelectionDialog } from "./DatabaseModuleSelectionDialog.js";
import { UserSecurityPasswordDialog } from "./UserSecurityPasswordDialog.js";
import { UserApiKeyDialog } from "./UserApiKeyDialog.js";
import { OauthClientSecretDialog } from "./OauthClientSecretDialog.js";
import { ModelActionParamsDialog } from "./ModelActionParamsDialog.js";
import { ExportDialog } from "../imex/ExportDialog.js";
import { ImportDialog } from "../imex/ImportDialog.js";

export const MODEL_DIALOGS = {
  DatabaseDuplicateDialog,
  DatabaseModuleSelectionDialog,
  UserSecurityPasswordDialog,
  UserApiKeyDialog,
  OauthClientSecretDialog,
  ModelActionParamsDialog,
  ExportDialog,
  ImportDialog,
};
