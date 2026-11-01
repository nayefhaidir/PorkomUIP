"use client";

import { useRef, useState } from "react";
import AnnouncementManager from "./announcement-manager";

type Item = { id: string; title: string; body: string; publishedAt: string; expiresAt: string | null; type: string; imageUrl: string | null };

export default function AnnouncementSequence({ managementTalk, news, canManageTalk, canManageNews, publicMode }: { managementTalk: Item[]; news: Item[]; canManageTalk: boolean; canManageNews: boolean; publicMode: boolean }) {
  const [openNews, setOpenNews] = useState(false);
  const sequenceComplete = useRef(false);
  return <>
    <AnnouncementManager category="MANAGEMENT_TALK" initial={managementTalk} canManage={canManageTalk} autoOpenFirstItem={publicMode && managementTalk.length > 0} onModalClose={() => { if (publicMode && !sequenceComplete.current && news.length) setOpenNews(true); }}/>
    <AnnouncementManager category="UIP_NEWS" initial={news} canManage={canManageNews} autoOpenFirstItem={publicMode && managementTalk.length === 0 && news.length > 0 || openNews} onModalClose={() => { sequenceComplete.current = true; }}/>
  </>;
}
