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
import { Input } from "@/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";

type Person = {
  code: string;
  name: string;
  department: string;
  position: string;
  status: string;
};

type Directory = {
  configured: boolean;
  freeform: boolean;
  items: Person[];
};

// The staff number decides whose transcript a conversation joins and whose
// memory it becomes, so it is picked from the company directory rather than
// typed. Typing stays available to whoever has been granted it, and to
// everyone when there is no directory to pick from.
export function ApiKeyStaffCombobox({
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

  const { data, isFetching } = useQuery({
    queryKey: ["staff-directory", keyword],
    queryFn: async () => {
      const params = new URLSearchParams({ size: "50" });
      if (keyword.trim()) params.set("keyword", keyword.trim());
      const { data } = await api.get<{ data?: Directory }>(
        `/api/token/staff-directory?${params.toString()}`,
      );
      return data.data ?? { configured: false, freeform: true, items: [] };
    },
    placeholderData: (previous) => previous,
  });

  // Until the directory has answered, a plain box is the honest fallback: it
  // never blocks someone from filling the field because a lookup is in flight.
  if (!data || !data.configured) {
    return (
      <Input
        value={value ?? ""}
        onChange={(event) => onValueChange(event.target.value)}
        disabled={disabled}
        placeholder={t("Enter a staff ID")}
      />
    );
  }

  const selected = data.items.find((person) => person.code === value);
  const label = selected
    ? `${selected.code} ${selected.name}`
    : value
      ? value
      : t("Pick from the staff directory");

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
            placeholder={t("Search by staff ID, name or department…")}
            value={keyword}
            onValueChange={setKeyword}
          />
          <CommandList>
            <CommandEmpty>
              {isFetching ? t("Searching…") : t("Nobody matches")}
            </CommandEmpty>
            {/* Whoever may write one freehand can commit whatever they typed,
                including a number the directory has never heard of. */}
            {data.freeform && keyword.trim() && keyword.trim() !== value ? (
              <CommandItem
                value={`__freeform__${keyword}`}
                onSelect={() => {
                  onValueChange(keyword.trim());
                  setOpen(false);
                }}
              >
                <Check className="mr-2 size-4 opacity-0" />
                {t('Use "{{value}}"', { value: keyword.trim() })}
              </CommandItem>
            ) : null}
            {data.items.map((person) => (
              <CommandItem
                key={person.code}
                value={person.code}
                onSelect={() => {
                  onValueChange(person.code);
                  setOpen(false);
                }}
              >
                <Check
                  className={cn(
                    "mr-2 size-4",
                    person.code === value ? "opacity-100" : "opacity-0",
                  )}
                />
                <span className="truncate">
                  <span className="font-mono">{person.code}</span> {person.name}
                </span>
                <span className="text-muted-foreground ml-auto truncate text-xs">
                  {person.department}
                  {person.status && person.status !== "在职"
                    ? ` · ${person.status}`
                    : ""}
                </span>
              </CommandItem>
            ))}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
