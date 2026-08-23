import { Copy } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { copyToClipboard } from "@/lib/copy-to-clipboard";

import { resetApiKey } from "../api";
import { ERROR_MESSAGES } from "../constants";
import { useApiKeys } from "./api-keys-provider";

// Resetting is not undoable and it stops whatever is using the old secret, so
// it asks first — and then shows the replacement once, because this is the only
// moment it can be copied.
export function ApiKeysResetDialog() {
  const { t } = useTranslation();
  const { open, setOpen, currentRow, triggerRefresh } = useApiKeys();
  const [isResetting, setIsResetting] = useState(false);
  const [issued, setIssued] = useState("");

  function close() {
    setOpen(null);
    setIssued("");
  }

  async function handleReset() {
    if (!currentRow) return;
    setIsResetting(true);
    try {
      const result = await resetApiKey(currentRow.id);
      if (!result.success || !result.data?.key) {
        toast.error(result.message || t(ERROR_MESSAGES.UNEXPECTED));
        return;
      }
      setIssued(result.data.key);
      triggerRefresh();
    } catch {
      toast.error(t(ERROR_MESSAGES.UNEXPECTED));
    } finally {
      setIsResetting(false);
    }
  }

  return (
    <AlertDialog
      open={open === "reset"}
      onOpenChange={(next) => (next ? setOpen("reset") : close())}
    >
      <AlertDialogContent>
        {issued ? (
          <>
            <AlertDialogHeader>
              <AlertDialogTitle>{t("New key issued")}</AlertDialogTitle>
              <AlertDialogDescription>
                {t(
                  "Copy it now — it is shown once. Everything else about this key is unchanged: quota, usage, staff ID, group and limits.",
                )}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <div className="bg-muted flex items-center gap-2 rounded-md p-3">
              <code className="min-w-0 flex-1 font-mono text-sm break-all">
                {issued}
              </code>
              <Button
                type="button"
                variant="outline"
                size="icon"
                className="shrink-0"
                onClick={async () => {
                  if (await copyToClipboard(issued)) toast.success(t("Copied"));
                }}
              >
                <Copy size={16} />
              </Button>
            </div>
            <AlertDialogFooter>
              <AlertDialogAction onClick={close}>{t("Done")}</AlertDialogAction>
            </AlertDialogFooter>
          </>
        ) : (
          <>
            <AlertDialogHeader>
              <AlertDialogTitle>
                {t("Reset this key’s secret?")}
              </AlertDialogTitle>
              <AlertDialogDescription>
                {t(
                  "Anything still using the old secret stops working immediately. The key keeps its quota, usage history, staff ID and settings — only the secret changes.",
                )}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel disabled={isResetting}>
                {t("Cancel")}
              </AlertDialogCancel>
              <AlertDialogAction
                disabled={isResetting}
                onClick={(event) => {
                  // Kept open so the replacement can be shown in place.
                  event.preventDefault();
                  void handleReset();
                }}
              >
                {isResetting ? t("Resetting…") : t("Reset Key")}
              </AlertDialogAction>
            </AlertDialogFooter>
          </>
        )}
      </AlertDialogContent>
    </AlertDialog>
  );
}
