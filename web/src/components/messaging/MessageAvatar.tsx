import type { MessageSenderType } from "./types";
import { getInitials } from "./utils";

export type MessageAvatarProps = {
  name?: string;
  avatarUrl?: string;
  senderType?: MessageSenderType;
};

export default function MessageAvatar({
  name = "Unknown",
  avatarUrl,
  senderType = "user",
}: MessageAvatarProps) {
  const bgColor =
    senderType === "agent"
      ? "bg-emerald-500/15 text-emerald-600 dark:text-emerald-300"
      : "bg-sky-500/15 text-sky-600 dark:text-sky-300";

  if (avatarUrl) {
    return (
      <img
        src={avatarUrl}
        alt={name}
        loading="lazy"
        decoding="async"
        className="h-8 w-8 rounded-full object-cover ring-2 ring-slate-200 dark:ring-slate-700"
      />
    );
  }

  return (
    <div
      className={`flex h-8 w-8 items-center justify-center rounded-full text-xs font-semibold ${bgColor}`}
      aria-label={senderType === "agent" ? "Agent avatar" : "User avatar"}
    >
      {getInitials(name)}
    </div>
  );
}
