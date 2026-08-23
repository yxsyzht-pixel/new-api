import { useQuery } from "@tanstack/react-query";
import { Check, ChevronsUpDown } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import {
  Command,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";

type OwnerOption = {
  id: number;
  username: string;
  display_name?: string;
};

// Whose account a key belongs to is a person, not a number. Asking for the
// numeric id meant looking it up on another page first; this searches the
// accounts by name and keeps the id as an implementation detail.
export function ApiKeyOwnerCombobox({
  value,
  onValueChange,
  disabled,
}: {
  value?: string;
  onValueChange: (value: string) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [keyword, setKeyword] = useState("");

  const { data: owners = [], isFetching } = useQuery({
    queryKey: ["key-owner-search", keyword],
    queryFn: async () => {
      const params = new URLSearchParams({ p: "1", size: "20" });
      if (keyword.trim()) params.set("keyword", keyword.trim());
      const { data } = await api.get<{
        data?: { items?: OwnerOption[] };
      }>(`/api/user/search?${params.toString()}`);
      return data.data?.items ?? [];
    },
    enabled: open,
    placeholderData: (previous) => previous,
  });

  const selected = owners.find((owner) => String(owner.id) === value);
  const label = selected
    ? `${selected.username}${selected.display_name ? ` (${selected.display_name})` : ""}`
    : value
      ? `#${value}`
      : t("Leave empty to create for yourself");

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={
          <Button
            type="button"
            variant="outline"
            role="combobox"
            aria-expanded={open}
            disabled={disabled}
            className={cn(
              "w-full justify-between font-normal",
              !value && "text-muted-foreground",
            )}
          />
        }
      >
        <span className="truncate">{label}</span>
        <ChevronsUpDown className="ml-2 size-4 shrink-0 opacity-50" />
      </PopoverTrigger>
      <PopoverContent
        className="w-(--radix-popover-trigger-width) p-0"
        align="start"
      >
        <Command shouldFilter={false}>
          <CommandInput
            placeholder={t("Search by username…")}
            value={keyword}
            onValueChange={setKeyword}
          />
          <CommandList>
            <CommandEmpty>
              {isFetching ? t("Searching…") : t("No accounts found")}
            </CommandEmpty>
            {value ? (
              <CommandItem
                value="__self__"
                onSelect={() => {
                  onValueChange("");
                  setOpen(false);
                }}
              >
                <Check className="mr-2 size-4 opacity-0" />
                {t("Create for yourself")}
              </CommandItem>
            ) : null}
            {owners.map((owner) => (
              <CommandItem
                key={owner.id}
                value={String(owner.id)}
                onSelect={() => {
                  onValueChange(String(owner.id));
                  setOpen(false);
                }}
              >
                <Check
                  className={cn(
                    "mr-2 size-4",
                    String(owner.id) === value ? "opacity-100" : "opacity-0",
                  )}
                />
                <span className="truncate">
                  {owner.username}
                  {owner.display_name ? ` (${owner.display_name})` : ""}
                </span>
                <span className="text-muted-foreground ml-auto text-xs">
                  #{owner.id}
                </span>
              </CommandItem>
            ))}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
