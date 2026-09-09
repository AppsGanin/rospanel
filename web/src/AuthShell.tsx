import type { ReactNode } from "react";
import { LangPills } from "./LangSwitch";
import { BrandLogo } from "./Logo";

// The frame every screen you meet before the panel shares: sign-in, the forced
// password change, and the first-run wizard. One narrow column on the recessed
// background, the wordmark above it, and everything else inside a single card.
export function AuthShell({
  children,
  wide,
}: {
  children: ReactNode;
  // The wizard needs room for its steps; sign-in does not.
  wide?: boolean;
}) {
  return (
    <div className="flex min-h-dvh items-center justify-center bg-gray-50 p-4">
      {/* The picker sits outside the card: an admin who can't read the form has
          nowhere else to reach it from — there is no account menu yet. */}
      <LangPills className="fixed right-3 top-3" />
      <div
        className={
          wide
            ? "w-full max-w-xl animate-fade-in-up"
            : "w-full max-w-[340px] animate-fade-in-up"
        }
      >
        <div className="mb-4 flex justify-center">
          <BrandLogo size={28} />
        </div>
        <div className="rounded-xl border border-brand-600/10 bg-white p-5">
          {children}
        </div>
      </div>
    </div>
  );
}
