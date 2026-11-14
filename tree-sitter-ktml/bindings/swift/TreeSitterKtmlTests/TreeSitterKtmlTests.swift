import XCTest
import SwiftTreeSitter
import TreeSitterKtml

final class TreeSitterKtmlTests: XCTestCase {
    func testCanLoadGrammar() throws {
        let parser = Parser()
        let language = Language(language: tree_sitter_ktml())
        XCTAssertNoThrow(try parser.setLanguage(language),
                         "Error loading KTML grammar")
    }
}
