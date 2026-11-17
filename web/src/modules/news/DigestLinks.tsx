import { ExternalLink } from "lucide-react";
import { safeExternalURL } from "../../shared/url";

export function DigestLinks({ items }: { items: { id: string; url: string; title: string; sourceName: string }[] }) {
  return (
    <ol className="news-brief-list">
      {items.map((item) => {
        const href = safeExternalURL(item.url);
        return (
          <li key={item.id} className="break-words">
            {href ? (
              <a href={href} target="_blank" rel="noopener noreferrer">
                {item.title}
                <ExternalLink className="ml-1 inline" size={12} aria-hidden="true" />
              </a>
            ) : (
              item.title
            )}
            <span className="news-note"> · {item.sourceName}</span>
          </li>
        );
      })}
    </ol>
  );
}
