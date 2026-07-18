import { Popover } from "antd";
import { QuestionCircleOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";

interface Props {
  page: "providers" | "traffic" | "dashboard" | "monitor" | "intent" | "settings";
}

export default function PageHelp({ page }: Props) {
  const { t } = useTranslation();
  const content = t(`help.${page}`, "");

  if (!content) return null;

  return (
    <Popover
      content={
        <div style={{ maxWidth: 300, fontSize: 12, lineHeight: 1.6, color: "var(--text-primary)" }}>
          {content}
        </div>
      }
      title={null}
      trigger="click"
      placement="bottom"
    >
      <span
        style={{
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          width: 18,
          height: 18,
          borderRadius: "50%",
          border: "1px solid #d1d5db",
          color: "var(--text-muted)",
          fontSize: 11,
          cursor: "pointer",
          transition: "all 0.15s",
          marginLeft: 6,
          verticalAlign: "middle",
        }}
        onMouseEnter={(e) => {
          e.currentTarget.style.borderColor = "var(--accent)";
          e.currentTarget.style.color = "var(--accent)";
          e.currentTarget.style.background = "var(--bg-active)";
        }}
        onMouseLeave={(e) => {
          e.currentTarget.style.borderColor = "var(--text-muted)";
          e.currentTarget.style.color = "var(--text-muted)";
          e.currentTarget.style.background = "transparent";
        }}
      >
        ?
      </span>
    </Popover>
  );
}
