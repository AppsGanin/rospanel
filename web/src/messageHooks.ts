import { createContext, useContext } from "react";

// Which message events an enabled webhook takes. The panel then offers to write to
// users the bot does not reach: the external system delivers it.
export interface MessageHooks {
  message: boolean;
  broadcast: boolean;
  autoMessage: boolean;
}

export const MessageHooksContext = createContext<MessageHooks>({
  message: false,
  broadcast: false,
  autoMessage: false,
});

export const useMessageHooks = () => useContext(MessageHooksContext);
