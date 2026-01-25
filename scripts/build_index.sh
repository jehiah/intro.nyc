#!/bin/bash


command -v jq >/dev/null || (echo "missing jq" && exit 1 )

set -e

SCRIPT=$(readlink -f "$0")
SCRIPTPATH=`dirname "$SCRIPT"`
cd $SCRIPTPATH/../../nyc_legislation

mkdir -p build

declare -a RECENT_YEARS
CURRENT_YEAR=$(date +%Y)
if ! [ -e introduction/${CURRENT_YEAR} ] && ! [ -e resolution/${CURRENT_YEAR} ]; then
    CURRENT_YEAR=$((CURRENT_YEAR - 1))
fi
START=${START:-"2024"}
while [ $START -le $CURRENT_YEAR ]; do
    RECENT_YEARS+=( "${START}" )
    ((START++))
done
echo "Building YEARS=${RECENT_YEARS[*]} set START=... for a different start year"

for YEAR in ${RECENT_YEARS[*]}; do
    if [ -e introduction/$YEAR ]; then
        echo "building index ${YEAR}.json"
        jq -c -s "map(del(.RTF,.GUID,.BodyID,.EnactmentDate,.PassedDate,.Version,.TextID,.StatusID,.TypeID,.TypeName,.AgendaDate,.Text,.Attachments)) | map(.History = ([.History[]? | del(.ActionID,.AgendaSequence,.MinutesSequence,.AgendaNumber,.Version,.MatterStatusID,.EventID,.LastModified,.ID,.BodyID,.Votes)] ))" introduction/$YEAR/????.json > build/${YEAR}.json;
        echo "building index ${YEAR}_votes.json"
        jq -c -s "map({File, StatusID, StatusName, Sponsors: ([.Sponsors[]? | {ID}]), History: ([.History[]? | select(.PassedFlagName != null) | {ActionID, Action, PassedFlagName, Votes: [(.Votes[]? | {ID, VoteID} ) ] }])}) " introduction/$YEAR/????.json > build/${YEAR}_votes.json;
    fi
    if [ -e resolution/$YEAR ]; then
        echo "building index resolution_${YEAR}.json"
        jq -c -s "map(del(.RTF,.GUID,.BodyID,.EnactmentDate,.PassedDate,.Version,.TextID,.StatusID,.TypeID,.TypeName,.AgendaDate,.Text,.Attachments)) | map(.History = ([.History[]? | del(.ActionID,.AgendaSequence,.MinutesSequence,.AgendaNumber,.Version,.MatterStatusID,.EventID,.LastModified,.ID,.BodyID,.Votes)] ))" resolution/$YEAR/????.json > build/resolution_${YEAR}.json;
        echo "building index resolution_${YEAR}_votes.json"
        jq -c -s "map({File, StatusID, StatusName, Sponsors: ([.Sponsors[]? | {ID}]), History: ([.History[]? | select(.PassedFlagName != null) | {ActionID, Action, PassedFlagName, Votes: [(.Votes[]? | {ID, VoteID} ) ] }])}) " resolution/$YEAR/????.json > build/resolution_${YEAR}_votes.json;
    fi

done


for YEAR in ${RECENT_YEARS[*]}; do
    if ! [ -e events/$YEAR ]; then
        continue
    fi
    echo "building events_${YEAR}.json"
    jq -c -s "map(del(.GUID,.VideoPath,.VideoStatus,.MinutesFile,.AgendaFile)) | map(.Items = ([.Items[]? | del(.ID,.GUID,.MatterID,.LastModified,.Version,.MinutesNote,.ActionText,.PassedFlag,.RollCall)] ))" events/$YEAR/*.json > build/events_${YEAR}.json;

    echo "building events_attendance_${YEAR}.json"
    jq -c -s "map({ID,BodyID,BodyName,Items}) | map(.Items = ([.Items[]? | select(.RollCallFlag == "1") | del(.ID,.GUID,.MatterID,.LastModified,.Version,.MinutesNote,.ActionText,.PassedFlag,.AgendaSequence,.MinutesSequence) |  .RollCall = ([.RollCall[]? | del(.FullName,.Slug,.Value,.Sort) ]) ] ))" events/$YEAR/*.json > build/events_attendance_${YEAR}.json;
done

echo "building people_active.json"
jq -c -s "map(select(.IsActive) | select(.End | fromdateiso8601 > now) | select(.Start | fromdateiso8601 < now) | del(.FirstName,.LastName,.GUID)) | map(.OfficeRecords = ([.OfficeRecords[]? | del(.GUID, .FullName, .PersonID, .LastModified) ]))" people/*.json > build/people_active.json

echo "building people_all.json"
jq -c -s "map(del(.FirstName,.LastName,.GUID)) | map(.OfficeRecords = ([.OfficeRecords[]? | del(.GUID, .FullName, .PersonID, .LastModified) ]))" people/*.json > build/people_all.json

echo "copying people_metadata.json"
cp people/appendix/people_metadata.json build/

echo "building local_laws.json"
# 27 'Introduced by Council',
# 33 'Amended by Committee',
# 32 'Approved by Committee',
# 68 'Approved by Council',
# ActionID=58 == City Charter Rule Adopted
# ActionID=57 == Signed Into Law by Mayor
# 59 == Vetoed by Mayor
# 5084 = Returned Unsigned by Mayor

jq -c -s 'map(select(.LocalLaw) | {File,LocalLaw,Title})' introduction/????/????.json > build/local_laws.json

# build a legislation index for each active legislator from the current session
for PERSON in people/*.json; do 
    PERSON_ID=$(jq -r "select(.End | fromdateiso8601 > now) | select (.Start | fromdateiso8601 < now) | .ID?" ${PERSON})
    # skip building index for inactive individuals
    if [ -z "${PERSON_ID}" ]; then
        continue
    fi
    echo "building legislation_$(basename $PERSON)"
    if [ -e introduction/2026 ]; then
        jq -c -s "map(select(.Sponsors[]?.ID == ${PERSON_ID})) | map(del(.RTF,.GUID,.TextID,.StatusID,.TypeID,.TypeName,.AgendaDate,.Attachments,.Text,.Version)) | map(.History = [(.History[]? | del(.Votes))])" introduction/2026/????.json > build/legislation_$(basename $PERSON .json).json;
    else
        jq -c -s "map(select(.Sponsors[]?.ID == ${PERSON_ID})) | map(del(.RTF,.GUID,.TextID,.StatusID,.TypeID,.TypeName,.AgendaDate,.Attachments,.Text,.Version)) | map(.History = [(.History[]? | del(.Votes))])" introduction/2024/????.json introduction/2025/????.json > build/legislation_$(basename $PERSON .json).json;
    fi
    echo "building resolution_$(basename $PERSON)"
    if [ -e resolution/2026 ]; then
        jq -c -s "map(select(.Sponsors[]?.ID == ${PERSON_ID})) | map(del(.RTF,.GUID,.TextID,.StatusID,.TypeID,.TypeName,.AgendaDate,.Attachments,.Text,.Version)) | map(.History = [(.History[]? | del(.Votes))])" resolution/2026/????.json > build/resolution_$(basename $PERSON .json).json;
    else
        jq -c -s "map(select(.Sponsors[]?.ID == ${PERSON_ID})) | map(del(.RTF,.GUID,.TextID,.StatusID,.TypeID,.TypeName,.AgendaDate,.Attachments,.Text,.Version)) | map(.History = [(.History[]? | del(.Votes))])" resolution/2024/????.json resolution/2025/????.json > build/resolution_$(basename $PERSON .json).json;
    fi
done

if [ -e introduction/2026 ]; then
    echo "building search_index_2026-2027.json"
    jq -c -s "map({File, Name, Title, Summary, StatusName, LastModified:  ([.History[]? | select(.ActionID == 27 or .ActionID == 33 or .ActionID == 32 or .ActionID == 68 or .ActionID == 58)])[-1]?.Date})" introduction/2026/????.json > build/search_index_2026-2027.json
else
    echo "building search_index_2024-2025.json"
    jq -c -s "map({File, Name, Title, Summary, StatusName, LastModified:  ([.History[]? | select(.ActionID == 27 or .ActionID == 33 or .ActionID == 32 or .ActionID == 68 or .ActionID == 58)])[-1]?.Date})" introduction/2024/????.json introduction/2025/????.json > build/search_index_2024-2025.json
fi


if [ -e resolution/2026 ]; then
    echo "building search_resolution_index_2026-2027.json"
    jq -c -s "map({File, Name, Title, Summary, StatusName, LastModified:  ([.History[]? | select(.ActionID == 27 or .ActionID == 33 or .ActionID == 32 or .ActionID == 68 or .ActionID == 58)])[-1]?.Date})" resolution/2026/????.json > build/search_index_resolution_2026-2027.json
else
    echo "building search_index_resolution_2024-2025.json"
    jq -c -s "map({File, Name, Title, Summary, StatusName, LastModified:  ([.History[]? | select(.ActionID == 27 or .ActionID == 33 or .ActionID == 32 or .ActionID == 68 or .ActionID == 58)])[-1]?.Date})" resolution/2024/????.json resolution/2025/????.json > build/search_index_resolution_2024-2025.json
fi

# backfill 2022-2023
# backfill 2018-2021
# backfill 2014-2017
# jq -c -s "map({File, Name, Title, Summary, StatusName, LastModified:  ([.History[]? | select(.ActionID == 27 or .ActionID == 33 or .ActionID == 32 or .ActionID == 68 or .ActionID == 58)])[-1]?.Date})" resolution/2022/????.json resolution/2023/????.json > build/search_index_resolution_2022-2023.json
# jq -c -s "map({File, Name, Title, Summary, StatusName, LastModified:  ([.History[]? | select(.ActionID == 27 or .ActionID == 33 or .ActionID == 32 or .ActionID == 68 or .ActionID == 58)])[-1]?.Date})" resolution/2018/????.json resolution/2019/????.json resolution/2020/????.json resolution/2021/????.json > build/search_index_resolution_2018-2021.json
# jq -c -s "map({File, Name, Title, Summary, StatusName, LastModified:  ([.History[]? | select(.ActionID == 27 or .ActionID == 33 or .ActionID == 32 or .ActionID == 68 or .ActionID == 58)])[-1]?.Date})" resolution/2014/????.json resolution/2015/????.json resolution/2016/????.json resolution/2017/????.json > build/search_index_resolution_2014-2017.json


for FILE in resubmit/*.json; do
    cp $FILE build/resubmit_$(basename $FILE)
done

echo "copying last_sync.json"
cp last_sync.json build/
