import { Download, Upload } from "lucide-react";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";

import { exportApiKeys, importApiKeys } from "../api";
import { useApiKeys } from "./api-keys-provider";

// Downloading from a page the browser is already authenticated for is simplest
// through a blob: the request carries the session the same way every other call
// does, and the file never becomes a URL anyone could share by accident.
function saveBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

export function ApiKeysSheetButtons() {
  const { t } = useTranslation();
  const { canManageAllKeys, allUsersScope, triggerRefresh } = useApiKeys();
  const [busy, setBusy] = useState<"export" | "import" | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  async function onExport() {
    setBusy("export");
    try {
      const scope = canManageAllKeys && allUsersScope ? "all" : undefined;
      const blob = await exportApiKeys(scope);
      const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, "");
      saveBlob(blob, `api-keys-${stamp}.xlsx`);
    } catch (error) {
      toast.error(String(error));
    } finally {
      setBusy(null);
    }
  }

  async function onImport(file: File) {
    setBusy("import");
    try {
      const result = await importApiKeys(file);
      if (!result.success) {
        toast.error(result.message || t("Import failed"));
        return;
      }
      const { updated = 0, skipped = 0, problems = [] } = result.data ?? {};
      if (skipped > 0) {
        // The rows that did not apply are the whole point of the report; a
        // count alone would leave someone hunting for which ones.
        toast.warning(
          t("Updated {{updated}}, skipped {{skipped}}", { updated, skipped }),
          { description: problems.slice(0, 5).join("\n"), duration: 12000 },
        );
      } else {
        toast.success(t("Updated {{updated}} keys", { updated }));
      }
      triggerRefresh();
    } catch (error) {
      toast.error(String(error));
    } finally {
      setBusy(null);
      if (fileInput.current) fileInput.current.value = "";
    }
  }

  return (
    <>
      <Button
        size="sm"
        variant="outline"
        disabled={busy !== null}
        onClick={onExport}
      >
        <Download className="h-4 w-4" />
        {busy === "export" ? t("Exporting…") : t("Export")}
      </Button>
      <Button
        size="sm"
        variant="outline"
        disabled={busy !== null}
        onClick={() => fileInput.current?.click()}
      >
        <Upload className="h-4 w-4" />
        {busy === "import" ? t("Importing…") : t("Import")}
      </Button>
      <input
        ref={fileInput}
        type="file"
        accept=".xlsx"
        className="hidden"
        onChange={(event) => {
          const file = event.target.files?.[0];
          if (file) void onImport(file);
        }}
      />
    </>
  );
}
