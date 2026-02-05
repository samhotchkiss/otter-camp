import { useMemo } from "react";
import { Link, useParams } from "react-router-dom";

type AgentStatus = "online" | "busy" | "idle" | "offline";

type Conversation = {
  id: string;
  name: string;
  role: string;
  status: AgentStatus;
  lastMessage: string;
  lastMessageTime: string;
  unreadCount: number;
  avatarUrl?: string;
  recent?: boolean;
};

type ChatMessage = {
  id: string;
  sender: "user" | "agent";
  content: string;
  time: string;
};

const conversations: Conversation[] = [
  {
    id: "frank",
    name: "Frank",
    role: "Chief of Staff",
    status: "online",
    lastMessage: "Drafted the weekly sync agenda.",
    lastMessageTime: "2m",
    unreadCount: 2,
    recent: true,
  },
  {
    id: "mina",
    name: "Mina",
    role: "Product Strategist",
    status: "busy",
    lastMessage: "Shared the Q2 roadmap outline.",
    lastMessageTime: "18m",
    unreadCount: 0,
    recent: true,
  },
  {
    id: "priya",
    name: "Priya",
    role: "Design Systems",
    status: "online",
    lastMessage: "Updated the token palette.",
    lastMessageTime: "45m",
    unreadCount: 1,
    recent: true,
  },
  {
    id: "derek",
    name: "Derek",
    role: "Engineering Lead",
    status: "idle",
    lastMessage: "Cluster deploy queued for 4pm.",
    lastMessageTime: "1h",
    unreadCount: 0,
  },
  {
    id: "leo",
    name: "Leo",
    role: "Data Analyst",
    status: "offline",
    lastMessage: "Retention chart is ready.",
    lastMessageTime: "2h",
    unreadCount: 0,
  },
  {
    id: "sasha",
    name: "Sasha",
    role: "Customer Success",
    status: "busy",
    lastMessage: "Escalated the priority ticket.",
    lastMessageTime: "4h",
    unreadCount: 3,
  },
];

const messagesByAgent: Record<string, ChatMessage[]> = {
  frank: [
    {
      id: "f-1",
      sender: "agent",
      content: "Morning! I pulled together a draft agenda for the sync.",
      time: "9:12 AM",
    },
    {
      id: "f-2",
      sender: "user",
      content: "Nice. Add a slot for the incident retro and partner updates.",
      time: "9:14 AM",
    },
    {
      id: "f-3",
      sender: "agent",
      content: "Done. Also added a 10-minute section for hiring pipeline.",
      time: "9:15 AM",
    },
    {
      id: "f-4",
      sender: "user",
      content: "Perfect. Send it out once Derek confirms.",
      time: "9:16 AM",
    },
  ],
  mina: [
    {
      id: "m-1",
      sender: "agent",
      content: "Uploaded the Q2 roadmap outline with three launch tracks.",
      time: "8:32 AM",
    },
    {
      id: "m-2",
      sender: "user",
      content: "Great. Can you highlight the bet sizes for each track?",
      time: "8:35 AM",
    },
    {
      id: "m-3",
      sender: "agent",
      content: "On it. I will send the summary in 30 minutes.",
      time: "8:36 AM",
    },
  ],
  priya: [
    {
      id: "p-1",
      sender: "agent",
      content: "Just pushed the updated token palette with warmer neutrals.",
      time: "7:58 AM",
    },
    {
      id: "p-2",
      sender: "user",
      content: "Love it. Can we use the golden accent for chat highlights?",
      time: "8:01 AM",
    },
    {
      id: "p-3",
      sender: "agent",
      content: "Yes, it is now mapped to #C9A86C for primary emphasis.",
      time: "8:03 AM",
    },
  ],
  derek: [
    {
      id: "d-1",
      sender: "agent",
      content: "Deploy is queued. Want a status ping when it completes?",
      time: "6:12 AM",
    },
    {
      id: "d-2",
      sender: "user",
      content: "Yes, and flag any anomalies in the logs.",
      time: "6:13 AM",
    },
  ],
  leo: [
    {
      id: "l-1",
      sender: "agent",
      content: "Retention dipped 2.3% week over week. I added notes.",
      time: "Yesterday",
    },
    {
      id: "l-2",
      sender: "user",
      content: "Thanks. Can you break that down by cohort size?",
      time: "Yesterday",
    },
  ],
  sasha: [
    {
      id: "s-1",
      sender: "agent",
      content: "Priority ticket escalated to on-call with full context.",
      time: "4:41 AM",
    },
    {
      id: "s-2",
      sender: "user",
      content: "Good. Keep the customer updated every 30 minutes.",
      time: "4:42 AM",
    },
    {
      id: "s-3",
      sender: "agent",
      content: "Understood. Drafted the next update already.",
      time: "4:44 AM",
    },
  ],
};

const statusColors: Record<AgentStatus, string> = {
  online: "bg-emerald-400",
  busy: "bg-amber-400",
  idle: "bg-otter-dark-muted",
  offline: "bg-slate-500",
};

function getInitials(name: string) {
  return name
    .split(" ")
    .map((part) => part[0])
    .slice(0, 2)
    .join("")
    .toUpperCase();
}

function ConversationsList({
  conversations,
  activeId,
}: {
  conversations: Conversation[];
  activeId: string;
}) {
  const recentConversations = conversations.filter((conversation) => conversation.recent);
  const allAgents = conversations;

  const renderConversation = (conversation: Conversation) => {
    const isActive = conversation.id === activeId;
    return (
      <Link
        key={`${conversation.id}-list`}
        to={`/chat/${conversation.id}`}
        className={`flex items-center gap-3 rounded-2xl border px-3 py-3 transition ${
          isActive
            ? "border-otter-dark-accent bg-otter-dark-surface-alt/90"
            : "border-otter-dark-border bg-otter-dark-bg/60 hover:border-otter-dark-accent/50 hover:bg-otter-dark-surface-alt/60"
        }`}
      >
        <div className="relative">
          {conversation.avatarUrl ? (
            <img
              src={conversation.avatarUrl}
              alt={conversation.name}
              className="h-11 w-11 rounded-full object-cover"
              loading="lazy"
              decoding="async"
            />
          ) : (
            <div className="flex h-11 w-11 items-center justify-center rounded-full bg-otter-dark-surface-alt text-sm font-semibold text-otter-dark-text">
              {getInitials(conversation.name)}
            </div>
          )}
          <span
            className={`absolute -bottom-0.5 -right-0.5 h-3 w-3 rounded-full border-2 border-otter-dark-surface ${statusColors[conversation.status]}`}
            aria-label={`${conversation.status} status`}
          />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-2">
            <p className="truncate text-sm font-semibold text-otter-dark-text">
              {conversation.name}
            </p>
            <span className="text-xs text-otter-dark-muted">
              {conversation.lastMessageTime}
            </span>
          </div>
          <p className="truncate text-xs text-otter-dark-muted">{conversation.lastMessage}</p>
        </div>
        {conversation.unreadCount > 0 && (
          <span className="flex h-6 min-w-[24px] items-center justify-center rounded-full bg-otter-dark-accent px-2 text-xs font-semibold text-otter-dark-bg">
            {conversation.unreadCount}
          </span>
        )}
      </Link>
    );
  };

  return (
    <div className="flex h-full flex-col gap-5 rounded-3xl border border-otter-dark-border bg-otter-dark-surface/95 p-4 shadow-xl">
      <div>
        <label className="text-xs font-semibold uppercase tracking-wide text-otter-dark-muted">
          Search
        </label>
        <input
          type="text"
          placeholder="Search agents or threads"
          className="mt-2 w-full rounded-2xl border border-otter-dark-border bg-otter-dark-bg/80 px-4 py-2.5 text-sm text-otter-dark-text placeholder:text-otter-dark-muted focus:border-otter-dark-accent focus:outline-none focus:ring-1 focus:ring-otter-dark-accent/40"
        />
      </div>

      <div>
        <p className="text-xs font-semibold uppercase tracking-wide text-otter-dark-muted">
          Recent Chats
        </p>
        <div className="mt-3 flex flex-col gap-2">
          {recentConversations.map(renderConversation)}
        </div>
      </div>

      <div className="flex-1">
        <p className="text-xs font-semibold uppercase tracking-wide text-otter-dark-muted">
          All Agents
        </p>
        <div className="mt-3 flex flex-col gap-2">
          {allAgents.map(renderConversation)}
        </div>
      </div>
    </div>
  );
}

function ChatHeader({ conversation }: { conversation: Conversation }) {
  return (
    <div className="flex items-center justify-between border-b border-otter-dark-border px-6 py-4">
      <div className="flex items-center gap-4">
        <div className="relative">
          <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-otter-dark-surface-alt text-lg font-semibold text-otter-dark-text">
            {getInitials(conversation.name)}
          </div>
          <span
            className={`absolute -bottom-1 -right-1 h-3.5 w-3.5 rounded-full border-2 border-otter-dark-surface ${statusColors[conversation.status]}`}
            aria-label={`${conversation.status} status`}
          />
        </div>
        <div>
          <p className="text-lg font-semibold text-otter-dark-text">{conversation.name}</p>
          <p className="text-sm text-otter-dark-muted">{conversation.role}</p>
        </div>
      </div>
      <div className="rounded-full border border-otter-dark-border bg-otter-dark-bg/70 px-3 py-1 text-xs text-otter-dark-muted">
        {conversation.status === "online" ? "Online now" : `Status: ${conversation.status}`}
      </div>
    </div>
  );
}

function MessageBubble({ message }: { message: ChatMessage }) {
  const isUser = message.sender === "user";
  const bubbleClasses = isUser
    ? "bg-otter-dark-accent text-otter-dark-bg"
    : "bg-otter-dark-surface-alt text-otter-dark-text border border-otter-dark-border";

  return (
    <div className={`flex ${isUser ? "justify-end" : "justify-start"}`}>
      <div className={`max-w-[70%] rounded-2xl px-4 py-3 text-sm ${bubbleClasses}`}>
        <p>{message.content}</p>
        <p className={`mt-2 text-xs ${isUser ? "text-otter-dark-bg/70" : "text-otter-dark-muted"}`}>
          {message.time}
        </p>
      </div>
    </div>
  );
}

function MessageInput() {
  return (
    <div className="border-t border-otter-dark-border px-6 py-4">
      <div className="flex items-center gap-3 rounded-2xl border border-otter-dark-border bg-otter-dark-surface-alt/80 px-3 py-2">
        <input
          type="text"
          placeholder="Write a message..."
          className="flex-1 bg-transparent px-2 py-2 text-sm text-otter-dark-text placeholder:text-otter-dark-muted focus:outline-none"
        />
        <button
          type="button"
          className="rounded-xl bg-otter-dark-accent px-4 py-2 text-sm font-semibold text-otter-dark-bg transition hover:bg-otter-dark-accent-hover"
        >
          Send
        </button>
      </div>
      <p className="mt-2 text-xs text-otter-dark-muted">
        Tip: Use short directives to keep your raft in sync.
      </p>
    </div>
  );
}

export default function ChatPage() {
  const { agentId } = useParams();
  const activeConversation = useMemo(() => {
    return conversations.find((conversation) => conversation.id === agentId) ?? conversations[0];
  }, [agentId]);
  const messages = messagesByAgent[activeConversation.id] ?? [];

  return (
    <div className="mx-auto flex w-full max-w-6xl flex-1 flex-col gap-6 px-4 pb-10 pt-6 sm:px-6 lg:px-8">
      <div className="flex flex-col gap-2">
        <h1 className="text-3xl font-semibold text-otter-dark-text">Agent Chat</h1>
        <p className="text-sm text-otter-dark-muted">
          Coordinate with your raft in real time using warm, focused threads.
        </p>
      </div>

      <div className="grid gap-6 lg:grid-cols-[320px,minmax(0,1fr)]">
        <ConversationsList conversations={conversations} activeId={activeConversation.id} />

        <section className="flex h-full min-h-[520px] flex-col overflow-hidden rounded-3xl border border-otter-dark-border bg-otter-dark-surface/95 shadow-xl">
          <ChatHeader conversation={activeConversation} />
          <div className="flex-1 space-y-4 overflow-y-auto px-6 py-6">
            {messages.map((message) => (
              <MessageBubble key={message.id} message={message} />
            ))}
          </div>
          <MessageInput />
        </section>
      </div>
    </div>
  );
}
