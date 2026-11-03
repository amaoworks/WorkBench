import { z } from "zod";

export const conversationSchema = z.object({
  id: z.string().min(1),
  title: z.string(),
  createdAt: z.string(),
  updatedAt: z.string(),
});

export const conversationsPageSchema = z.object({
  items: z.array(conversationSchema),
  nextCursor: z.string().optional(),
});

const conversationMessageSchema = z.object({
  role: z.enum(["user", "assistant"]),
  content: z.string(),
});

export const conversationMessagesPageSchema = z.object({
  messages: z.array(conversationMessageSchema),
  nextCursor: z.string().optional(),
});

export type Conversation = z.infer<typeof conversationSchema>;
