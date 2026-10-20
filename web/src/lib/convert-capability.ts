import { createContext, useContext } from "react";

/**
 * Conversion capability reported by `GET /convert/providers` (CONFIG-V2 §6).
 * Older backends without the endpoint keep conversion visible (defaults true).
 */
export interface ConvertCapability {
  /** False when no conversion backend (ffmpeg / cloudconvert) is available. */
  canConvert: boolean;
  /** Provider names reported by the backend, e.g. ["ffmpeg"]. */
  providers: string[];
}

export const ConvertCapabilityContext = createContext<ConvertCapability>({
  canConvert: true,
  providers: []
});

export function useConvertCapability(): ConvertCapability {
  return useContext(ConvertCapabilityContext);
}
