import { useQuery } from "@tanstack/react-query";
import { Loader2, RefreshCw, Search, UserRound } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";

export type StaffPerson = {
  code: string;
  name: string;
  department: string;
  position: string;
  status: string;
};

type Directory = {
  configured: boolean;
  freeform: boolean;
  items: StaffPerson[];
};

export function useStaffDirectory(keyword: string, enabled: boolean) {
  return useQuery({
    queryKey: ["staff-directory", keyword],
    queryFn: async () => {
      const params = new URLSearchParams({ size: "200" });
      if (keyword.trim()) params.set("keyword", keyword.trim());
      const { data } = await api.get<{ data?: Directory }>(
        `/api/token/staff-directory?${params.toString()}`,
      );
      return data.data ?? { configured: false, freeform: true, items: [] };
    },
    enabled,
    placeholderData: (previous) => previous,
  });
}

// Choosing a colleague is a browsing task — you scan for a name, a department,
// a face you recognise. A dropdown under a text box is too small for that, so
// it gets the room a list needs.
export function StaffPickerDialog({
  open,
  onOpenChange,
  onPick,
  selected,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onPick: (person: StaffPerson) => void;
  selected?: string;
}) {
  const { t } = useTranslation();
  const [keyword, setKeyword] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const { data, isFetching, refetch } = useStaffDirectory(keyword, open);

  async function onRefresh() {
    setRefreshing(true);
    try {
      const { data } = await api.post<{ success: boolean; message: string }>(
        "/api/token/staff-directory/refresh",
        {},
      );
      if (!data?.success) {
        toast.error(data?.message ?? t("Request failed"));
        return;
      }
      await refetch();
    } catch (error) {
      toast.error(String(error));
    } finally {
      setRefreshing(false);
    }
  }

  const people = data?.items ?? [];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[80vh] flex-col gap-0 p-0 sm:max-w-lg">
        <DialogHeader className="flex-row items-center justify-between gap-2 border-b p-4">
          <DialogTitle>{t("Pick a person")}</DialogTitle>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-8"
            disabled={refreshing}
            title={t("Refresh from HR")}
            onClick={onRefresh}
          >
            <RefreshCw className={cn("size-4", refreshing && "animate-spin")} />
          </Button>
        </DialogHeader>

        <div className="border-b p-3">
          <div className="relative">
            <Search className="text-muted-foreground absolute start-3 top-1/2 size-4 -translate-y-1/2" />
            <Input
              autoFocus
              value={keyword}
              onChange={(event) => setKeyword(event.target.value)}
              placeholder={t("Search by staff ID, name or department…")}
              className="ps-9"
            />
          </div>
        </div>

        <ScrollArea className="flex-1 overflow-y-auto">
          {people.length === 0 ? (
            <p className="text-muted-foreground p-8 text-center text-sm">
              {isFetching ? t("Searching…") : t("Nobody matches")}
            </p>
          ) : (
            <ul className="divide-y">
              {people.map((person) => (
                <li key={person.code}>
                  <button
                    type="button"
                    onClick={() => {
                      onPick(person);
                      onOpenChange(false);
                    }}
                    className={cn(
                      "hover:bg-muted/60 flex w-full items-center gap-3 p-3 text-start transition-colors",
                      person.code === selected && "bg-muted",
                    )}
                  >
                    <span className="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-md">
                      <UserRound className="size-5" />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="flex items-center gap-2">
                        <span className="truncate font-medium">
                          {person.name}
                        </span>
                        {person.status && person.status !== "在职" ? (
                          <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">
                            {person.status}
                          </span>
                        ) : null}
                      </span>
                      <span className="text-muted-foreground block truncate text-sm">
                        {[person.code, person.position, person.department]
                          .filter(Boolean)
                          .join("-")}
                      </span>
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
          {isFetching && people.length > 0 ? (
            <p className="text-muted-foreground flex items-center justify-center gap-2 p-2 text-xs">
              <Loader2 className="size-3 animate-spin" />
              {t("Searching…")}
            </p>
          ) : null}
        </ScrollArea>
      </DialogContent>
    </Dialog>
  );
}
